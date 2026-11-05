// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package mutagensync

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Prabhudas111/protolift/internal/shared/errs"
	"github.com/Prabhudas111/protolift/internal/shared/retry"
)

// Version is the Mutagen release protolift is tested with. It is pinned
// with checksums: a download that does not match is never run.
const Version = "v0.18.1"

var checksums = map[string]string{
	"darwin_amd64":  "7d06f7d8fcfe90bc7e55cc834a2f2f20c2e0af9ea9bc35911fc4341ad56a9bbf",
	"darwin_arm64":  "6f810416d9e5fc4fd5e18431146f8b3c5a2056ba5a24f76c1e66da86eb3257e2",
	"linux_amd64":   "7735286c778cc438418209f24d03a64f3a0151c8065ef0fe079cfaf093af6f8f",
	"linux_arm64":   "bcba735aebf8cbc11da9b3742118a665599ac697fa06bc5751cac8dcd540db8a",
	"windows_amd64": "3e237e77f69959ed520a0f877330a431507bb0a85d9da7919764ba0c87b702c7",
	"windows_arm64": "9ac53447e46f019be9d37f49c00eeed8635966b885ed29ef06b3ff19afdee532",
}

// Download limits.
const (
	downloadTimeout = 5 * time.Minute
	maxArchiveBytes = 200 << 20
)

// ensureTool returns the path of the pinned Mutagen binary, downloading
// and checking it the first time.
func ensureTool(ctx context.Context, root string) (string, error) {
	platform := runtime.GOOS + "_" + runtime.GOARCH
	sum, ok := checksums[platform]
	if !ok {
		return "", errs.New(errs.CodeToolMissing, "file sync is not available for "+platform, "protolift supports macOS, Linux and Windows on Intel and ARM")
	}
	dir := filepath.Join(root, "tools", "mutagen-"+Version)
	bin := filepath.Join(dir, "mutagen")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if _, err := os.Stat(bin); err == nil {
		if _, err := os.Stat(filepath.Join(dir, "mutagen-agents.tar.gz")); err == nil {
			return bin, nil
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", errs.Wrap(errs.CodeInternal, err, "cannot create "+dir, "")
	}
	url := fmt.Sprintf("https://github.com/mutagen-io/mutagen/releases/download/%s/mutagen_%s_%s.tar.gz", Version, platform, Version)
	archive := filepath.Join(dir, "download.tar.gz")
	err := retry.Do(ctx, retry.Default, func(ctx context.Context) (bool, error) {
		err := download(ctx, url, archive)
		return err != nil && errs.CodeOf(err) == errs.CodeNetwork, err
	})
	if err != nil {
		return "", err
	}
	defer os.Remove(archive)
	if err := verify(archive, sum); err != nil {
		return "", err
	}
	if err := extract(archive, dir); err != nil {
		return "", err
	}
	return bin, nil
}

func download(ctx context.Context, url, dst string) error {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return errs.Wrap(errs.CodeInternal, err, "bad download address", "")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errs.Wrap(errs.CodeNetwork, err, "could not download the file-sync tool", "check your internet connection")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errs.New(errs.CodeNetwork, fmt.Sprintf("downloading the file-sync tool failed (HTTP %d)", resp.StatusCode), "try again in a minute")
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot write "+dst, "")
	}
	_, err = io.Copy(f, io.LimitReader(resp.Body, maxArchiveBytes))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return errs.Wrap(errs.CodeNetwork, err, "the file-sync download was interrupted", "try again")
	}
	return nil
}

func verify(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot read the download", "")
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot read the download", "")
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return errs.New(errs.CodeInternal, "the file-sync download does not match its checksum, so it was not used",
			"try again; if it keeps failing, report it (someone may be tampering with the download)")
	}
	return nil
}

// extract unpacks only the two files protolift needs, refusing any path
// that tries to leave dir.
func extract(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot open the download", "")
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return errs.Wrap(errs.CodeInternal, err, "the download is not a valid archive", "")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	want := map[string]bool{"mutagen": true, "mutagen.exe": true, "mutagen-agents.tar.gz": true}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return errs.Wrap(errs.CodeInternal, err, "the download is not a valid archive", "")
		}
		name := filepath.Base(h.Name)
		if h.Typeflag != tar.TypeReg || !want[name] || strings.Contains(h.Name, "..") {
			continue
		}
		dst := filepath.Join(dir, name)
		out, err := os.OpenFile(dst+".tmp", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
		if err != nil {
			return errs.Wrap(errs.CodeInternal, err, "cannot write "+dst, "")
		}
		_, err = io.Copy(out, io.LimitReader(tr, maxArchiveBytes))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return errs.Wrap(errs.CodeInternal, err, "cannot unpack "+name, "")
		}
		if err := os.Rename(dst+".tmp", dst); err != nil {
			return errs.Wrap(errs.CodeInternal, err, "cannot place "+name, "")
		}
	}
}
