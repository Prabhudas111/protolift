// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

// Package registry implements ports.ImageInspector over the OCI
// distribution API, so the laptop needs no Docker to learn which chips an
// image supports. Any failure (private image, rate limit, offline) is
// reported as "unknown", and the domain then picks Intel, which always works.
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/env/ports"
)

const (
	requestTimeout = 8 * time.Second
	maxBody        = 4 << 20
	dockerHub      = "registry-1.docker.io"
)

var accept = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

// Inspector implements ports.ImageInspector.
type Inspector struct{ c *http.Client }

var _ ports.ImageInspector = (*Inspector)(nil)

// New returns an inspector with bounded timeouts.
func New() *Inspector { return &Inspector{c: &http.Client{Timeout: requestTimeout}} }

// Ref is a parsed image reference.
type Ref struct{ Host, Repo, Tag string }

// Parse splits an image reference the way Docker does: no host means
// Docker Hub, and a one-part Docker Hub name lives under library/.
func Parse(image string) (Ref, error) {
	if image == "" || strings.ContainsAny(image, "${} ") {
		return Ref{}, errors.New("not a plain image name")
	}
	name, tag := image, "latest"
	if at := strings.Index(name, "@"); at >= 0 {
		name, tag = name[:at], name[at+1:]
	} else if c := strings.LastIndex(name, ":"); c > strings.LastIndex(name, "/") {
		name, tag = name[:c], name[c+1:]
	}
	host, repo := dockerHub, name
	if i := strings.Index(name, "/"); i >= 0 {
		first := name[:i]
		if strings.ContainsAny(first, ".:") || first == "localhost" {
			host, repo = first, name[i+1:]
		}
	}
	if host == dockerHub && !strings.Contains(repo, "/") {
		repo = "library/" + repo
	}
	if host == "docker.io" || host == "index.docker.io" {
		host = dockerHub
	}
	return Ref{host, repo, tag}, nil
}

// Support asks the registry which chips the image has.
func (in *Inspector) Support(ctx context.Context, image string) domain.ArchSupport {
	ref, err := Parse(image)
	if err != nil {
		return domain.ArchSupport{}
	}
	body, mediaType, err := in.get(ctx, ref, "/manifests/"+ref.Tag, accept)
	if err != nil {
		return domain.ArchSupport{}
	}
	var m struct {
		Manifests []struct {
			Platform struct {
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
			} `json:"platform"`
		} `json:"manifests"`
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return domain.ArchSupport{}
	}
	var s domain.ArchSupport
	if strings.Contains(mediaType, "index") || strings.Contains(mediaType, "list") || len(m.Manifests) > 0 {
		for _, x := range m.Manifests {
			if x.Platform.OS != "" && x.Platform.OS != "linux" {
				continue
			}
			mark(&s, x.Platform.Architecture)
		}
		s.Known = s.ARM || s.Intel
		return s
	}
	// A single-platform image: the chip is in its config blob.
	cfg, _, err := in.get(ctx, ref, "/blobs/"+m.Config.Digest, "*/*")
	if err != nil {
		return domain.ArchSupport{}
	}
	var c struct {
		Architecture string `json:"architecture"`
	}
	if json.Unmarshal(cfg, &c) != nil {
		return domain.ArchSupport{}
	}
	mark(&s, c.Architecture)
	s.Known = s.ARM || s.Intel
	return s
}

func mark(s *domain.ArchSupport, arch string) {
	switch arch {
	case "arm64":
		s.ARM = true
	case "amd64":
		s.Intel = true
	}
}

// get fetches a registry path, doing the anonymous token dance the
// distribution spec describes when the registry asks for it.
func (in *Inspector) get(ctx context.Context, ref Ref, p, acceptHdr string) ([]byte, string, error) {
	u := "https://" + ref.Host + "/v2/" + ref.Repo + p
	resp, err := in.do(ctx, u, acceptHdr, "")
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("Www-Authenticate")
		drain(resp)
		token, err := in.token(ctx, challenge)
		if err != nil {
			return nil, "", err
		}
		if resp, err = in.do(ctx, u, acceptHdr, token); err != nil {
			return nil, "", err
		}
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("registry said %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	return b, resp.Header.Get("Content-Type"), err
}

func (in *Inspector) do(ctx context.Context, u, acceptHdr, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", acceptHdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return in.c.Do(req)
}

// token gets an anonymous pull token from a Bearer challenge.
func (in *Inspector) token(ctx context.Context, challenge string) (string, error) {
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		return "", errors.New("registry needs a login")
	}
	params := map[string]string{}
	for _, part := range splitParams(challenge[len("bearer "):]) {
		k, v, ok := strings.Cut(part, "=")
		if ok {
			params[strings.ToLower(strings.TrimSpace(k))] = strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	realm, err := url.Parse(params["realm"])
	if err != nil || realm.Scheme != "https" {
		return "", errors.New("registry token address is not https")
	}
	q := realm.Query()
	for _, k := range []string{"service", "scope"} {
		if params[k] != "" {
			q.Set(k, params[k])
		}
	}
	realm.RawQuery = q.Encode()
	resp, err := in.do(ctx, realm.String(), "application/json", "")
	if err != nil {
		return "", err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token request said %d", resp.StatusCode)
	}
	var t struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&t); err != nil {
		return "", err
	}
	if t.Token != "" {
		return t.Token, nil
	}
	return t.AccessToken, nil
}

// splitParams splits k="v",k2="v,2" on commas outside quotes.
func splitParams(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case r == ',' && !inQuote:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	return append(out, cur.String())
}

func drain(r *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, maxBody)) // gate-allow: draining a response body for connection reuse; the error has no use
	r.Body.Close()
}
