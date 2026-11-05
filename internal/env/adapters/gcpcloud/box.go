// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package gcpcloud

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/env/ports"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

const diskGB = "60GB"

// instance is the part of `gcloud compute instances describe` protolift reads.
type instance struct {
	Name        string            `json:"name"`
	Status      string            `json:"status"`
	MachineType string            `json:"machineType"`
	Zone        string            `json:"zone"`
	Labels      map[string]string `json:"labels"`
	LastStart   string            `json:"lastStartTimestamp"`
	LastStop    string            `json:"lastStopTimestamp"`
	CPUPlatform string            `json:"cpuPlatform"`
}

func (c *Cloud) toBox(i instance) domain.Box {
	t := lastPart(i.MachineType)
	b := domain.Box{
		ID: i.Name, Name: i.Name, EnvID: i.Labels[LabelEnv], InstanceType: t,
		Region: c.region, Location: lastPart(i.Zone), State: state(i.Status), Arch: domain.ArchIntel,
	}
	if _, a, ok := c.SizeOf(t); ok {
		b.Arch = a
	} else if strings.HasPrefix(t, "t2a-") || strings.HasPrefix(t, "c4a-") {
		b.Arch = domain.ArchARM
	}
	if ts, err := time.Parse(time.RFC3339, i.LastStart); err == nil {
		b.StartedAt = ts
	}
	if ts, err := time.Parse(time.RFC3339, i.LastStop); err == nil {
		b.StoppedAt = ts
	}
	return b
}

// state maps Compute Engine status to protolift words. A stopped VM is
// TERMINATED in Compute Engine terms; its disk is kept.
func state(s string) domain.BoxState {
	switch s {
	case "RUNNING":
		return domain.BoxRunning
	case "PROVISIONING", "STAGING", "REPAIRING":
		return domain.BoxPending
	case "STOPPING", "SUSPENDING":
		return domain.BoxStopping
	}
	return domain.BoxStopped
}

// ListBoxes returns protolift VMs in the region. envID "" means all.
func (c *Cloud) ListBoxes(ctx context.Context, envID string) ([]domain.Box, error) {
	var list []instance
	if err := c.run(ctx, callTimeout, &list, "compute", "instances", "list", "--filter", c.listFilter(envID)); err != nil {
		return nil, err
	}
	boxes := make([]domain.Box, 0, len(list))
	for _, i := range list {
		boxes = append(boxes, c.toBox(i))
	}
	sort.Slice(boxes, func(i, j int) bool { return boxes[i].Name < boxes[j].Name })
	return boxes, nil
}

// listFilter selects protolift VMs in the region. An instance's zone is a
// full URL (…/zones/us-central1-a), so it matches "/zones/<region>-" inside
// it, not the start of the string.
func (c *Cloud) listFilter(envID string) string {
	f := "labels." + LabelManaged + "=true AND zone~/zones/" + c.region + "-"
	if envID != "" {
		f += " AND labels." + LabelEnv + "=" + envID
	}
	return f
}

// FindBox returns the env's box; two for one env is a CONFLICT to resolve.
func (c *Cloud) FindBox(ctx context.Context, envID string) (domain.Box, bool, error) {
	boxes, err := c.ListBoxes(ctx, envID)
	if err != nil || len(boxes) == 0 {
		return domain.Box{}, false, err
	}
	if len(boxes) > 1 {
		return domain.Box{}, false, errs.New(errs.CodeConflict, fmt.Sprintf("found %d boxes for this project", len(boxes)),
			"delete the extra ones in the Google Cloud console, or run `protolift down` and `protolift up`")
	}
	return boxes[0], true, nil
}

// boxName is unique per env and fits Compute Engine's 63-character limit.
func boxName(name, envID string) string {
	n := "protolift-" + name
	if len(n) > 50 {
		n = strings.TrimRight(n[:50], "-")
	}
	return n + "-" + envID[:6]
}

// CreateBox makes the VM: no service account, ssh key for the box user in
// its own metadata only (project-wide keys blocked), ssh reachable only from
// IAP, and a hard run limit that stops it even if the box script fails.
func (c *Cloud) CreateBox(ctx context.Context, s ports.BoxSpec) (domain.Box, error) {
	fw, err := c.firewall(ctx)
	if err != nil {
		return domain.Box{}, err
	}
	zones, err := c.zonesFor(ctx, s.InstanceType)
	if err != nil {
		return domain.Box{}, err
	}
	if len(zones) == 0 {
		return domain.Box{}, errs.New(errs.CodeCapacity, c.region+" does not offer "+s.InstanceType, "pick another --size, --arch or --region")
	}
	tmp, err := os.MkdirTemp("", "protolift-gcp-")
	if err != nil {
		return domain.Box{}, errs.Wrap(errs.CodeInternal, err, "cannot create a temp folder", "")
	}
	defer os.RemoveAll(tmp)
	startup, keys := filepath.Join(tmp, "startup.sh"), filepath.Join(tmp, "ssh-keys")
	if err := os.WriteFile(startup, []byte(s.BootScript), 0o600); err != nil {
		return domain.Box{}, errs.Wrap(errs.CodeInternal, err, "cannot write the boot script", "")
	}
	if err := os.WriteFile(keys, []byte(domain.BoxUser+":"+strings.TrimSpace(s.PubKey)+"\n"), 0o600); err != nil {
		return domain.Box{}, errs.Wrap(errs.CodeInternal, err, "cannot write the ssh key", "")
	}
	image := "ubuntu-2404-lts-amd64"
	if s.Arch == domain.ArchARM {
		image = "ubuntu-2404-lts-arm64"
	}
	name := boxName(s.Name, s.EnvID)
	args := []string{"compute", "instances", "create", name, "--zone", zones[0], "--machine-type", s.InstanceType,
		"--image-family", image, "--image-project", "ubuntu-os-cloud", "--boot-disk-size", diskGB, "--boot-disk-type", "pd-balanced",
		"--network", lastPart(fw.Network), "--tags", NetworkTag, "--no-service-account", "--no-scopes",
		"--labels", fmt.Sprintf("%s=true,%s=%s,%s=%s", LabelManaged, LabelEnv, s.EnvID, LabelProject, s.Name),
		"--metadata", "enable-oslogin=FALSE,block-project-ssh-keys=TRUE",
		"--metadata-from-file", "startup-script=" + startup + ",ssh-keys=" + keys}
	if s.MaxHours > 0 {
		args = append(args, fmt.Sprintf("--max-run-duration=%dh", s.MaxHours), "--instance-termination-action=STOP")
	}
	var created []instance
	if err := c.run(ctx, createTimeout, &created, args...); err != nil {
		return domain.Box{}, err
	}
	if len(created) == 0 {
		return domain.Box{}, errs.New(errs.CodeInternal, "gcloud created the box but did not describe it", "run `protolift status`")
	}
	return c.toBox(created[0]), nil
}

func (c *Cloud) instanceOp(ctx context.Context, timeout time.Duration, b domain.Box, op string, extra ...string) error {
	args := append([]string{"compute", "instances", op, b.ID, "--zone", b.Location}, extra...)
	return c.run(ctx, timeout, nil, args...)
}

// StartBox starts a stopped box.
func (c *Cloud) StartBox(ctx context.Context, b domain.Box) error {
	if b.State == domain.BoxRunning || b.State == domain.BoxPending {
		return nil
	}
	return c.instanceOp(ctx, createTimeout, b, "start")
}

// StopBox stops the box and keeps its disk.
func (c *Cloud) StopBox(ctx context.Context, b domain.Box) error {
	if b.State == domain.BoxStopped {
		return nil
	}
	return c.instanceOp(ctx, createTimeout, b, "stop")
}

// DeleteBox deletes the VM and its boot disk.
func (c *Cloud) DeleteBox(ctx context.Context, b domain.Box) error {
	err := c.instanceOp(ctx, createTimeout, b, "delete", "--delete-disks=all")
	if isCode(err, errs.CodeNotFound) {
		return nil
	}
	return err
}

// ResizeBox stops the VM and changes its machine type; the disk stays.
func (c *Cloud) ResizeBox(ctx context.Context, b domain.Box, instanceType string) error {
	if err := c.StopBox(ctx, b); err != nil {
		return err
	}
	return c.instanceOp(ctx, createTimeout, b, "set-machine-type", "--machine-type", instanceType)
}

// WaitReachable waits for RUNNING. The application then waits out the few
// seconds before ssh through IAP accepts connections.
func (c *Cloud) WaitReachable(ctx context.Context, b domain.Box, log func(string)) error {
	deadline := time.Now().Add(waitRunning)
	for time.Now().Before(deadline) {
		var i instance
		if err := c.run(ctx, callTimeout, &i, "compute", "instances", "describe", b.ID, "--zone", b.Location); err != nil {
			return err
		}
		if i.Status == "RUNNING" {
			log("box running; Google's private tunnel (IAP) is ready")
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollEvery):
		}
	}
	return errs.New(errs.CodeTimeout, "the box did not start within 10 minutes", "check the VM in the Google Cloud console")
}

// HostName: ssh's HostName is the VM name; IAP resolves it in the zone.
func (c *Cloud) HostName(b domain.Box) string { return b.ID }

// ProxyCommand opens Google's IAP tunnel to the VM's ssh port. gcloud is
// given by full path, so it also works where ssh does not search PATH.
func (c *Cloud) ProxyCommand(b domain.Box, _ string) string {
	return fmt.Sprintf(`%s compute start-iap-tunnel %%h %%p --listen-on-stdin --zone %s --project %s --verbosity=warning`,
		quote(c.gcloud), b.Location, c.project)
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }
