// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

// Package boxscripts implements ports.Scripts: the bash that runs on the
// box. Every value that comes from the user (paths, commands, names) goes
// through quote() or systemdQuote(), so nothing is ever spliced raw into a
// shell or a unit file.
package boxscripts

import (
	"bytes"
	_ "embed"
	"fmt"
	"path"
	"strconv"
	"strings"
	"text/template"

	"github.com/Prabhudas111/protolift/internal/env/domain"
	"github.com/Prabhudas111/protolift/internal/env/ports"
)

//go:embed files/boot.sh.tmpl
var bootTmpl string

//go:embed files/idle.sh
var idleScript string

const idleConfigPath = "/etc/protolift/idle.conf"

// lockfiles decide when setup runs again.
var lockfiles = []string{
	"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lockb", "go.sum", "Cargo.lock",
	"poetry.lock", "uv.lock", "Pipfile.lock", "requirements.txt", "Gemfile.lock", "composer.lock",
}

// Scripts implements ports.Scripts.
type Scripts struct{ boot *template.Template }

var _ ports.Scripts = (*Scripts)(nil)

// New parses the embedded boot template once.
func New() (*Scripts, error) {
	t, err := template.New("boot").Option("missingkey=error").Parse(bootTmpl)
	if err != nil {
		return nil, fmt.Errorf("boot template: %w", err)
	}
	return &Scripts{boot: t}, nil
}

// Boot renders the first-boot script.
func (s *Scripts) Boot(st domain.Settings) string {
	var b bytes.Buffer
	// The template and its data are fixed at build time; tests prove it
	// renders, so an error here can only be a programming mistake.
	if err := s.boot.Execute(&b, map[string]string{
		"User": domain.BoxUser, "WorkRoot": domain.WorkRoot,
		"IdleConfig": string(s.IdleConfig(st, 0)), "IdleScript": idleScript,
	}); err != nil {
		panic(fmt.Sprintf("boot template: %v", err))
	}
	return b.String()
}

// IdleConfig is plain KEY=number lines; the idle script reads numbers only.
func (s *Scripts) IdleConfig(st domain.Settings, budgetMinutes int) []byte {
	return []byte(fmt.Sprintf("IDLE_MINUTES=%d\nMAX_HOURS=%d\nBUDGET_MINUTES=%d\n", st.IdleMinutes, st.MaxHours, max(budgetMinutes, 0)))
}

// IdleConfigPath is where IdleConfig lives on the box.
func (s *Scripts) IdleConfigPath() string { return idleConfigPath }

// BootState prints ready, waiting, or failed and the boot log tail.
func (s *Scripts) BootState() string {
	return `if [ -f /var/lib/protolift/ready ]; then echo ready
elif [ -f /var/lib/protolift/failed ]; then echo failed; sudo tail -n 25 /var/lib/protolift/boot.log
else echo waiting; fi`
}

// SetupHash hashes the setup command and every lockfile in the folder.
func (s *Scripts) SetupHash(f domain.Folder) string {
	files := make([]string, len(lockfiles))
	for i, l := range lockfiles {
		files[i] = quote(l)
	}
	return fmt.Sprintf(`cd %s && { printf '%%s' %s; for f in %s; do [ -f "$f" ] && cat "$f"; done; } | sha256sum | cut -c1-16`,
		quote(f.RemotePath), quote(f.Setup), strings.Join(files, " "))
}

// Setup runs the folder's setup command in a login shell (for the PATH).
func (s *Scripts) Setup(f domain.Folder) string {
	return fmt.Sprintf("cd %s && bash -lc %s", quote(f.RemotePath), quote(f.Setup))
}

// UnitName is the system service of a run-command folder.
func UnitName(envName string, f domain.Folder) string {
	return fmt.Sprintf("protolift-%s-%s.service", domain.Slug(envName), domain.Slug(path.Base(f.RemotePath)))
}

// Start brings a folder up. Compose: `up -d --build`. Run command: a system
// service that restarts on failure; it is only restarted when its unit
// changed, so a second `up` does not interrupt a running dev server.
func (s *Scripts) Start(f domain.Folder, envName string) string {
	if f.IsCompose() {
		return fmt.Sprintf("cd %s && docker compose -f %s up -d --build --remove-orphans", quote(f.RemotePath), quote(f.Compose))
	}
	unit := UnitName(envName, f)
	body := fmt.Sprintf(`[Unit]
Description=protolift %s %s
After=network-online.target docker.service

[Service]
User=%s
WorkingDirectory=%s
ExecStart=/bin/bash -lc %s
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
`, domain.Slug(envName), domain.Slug(path.Base(f.RemotePath)), domain.BoxUser, systemdPath(f.RemotePath), systemdQuote(f.Run))
	return fmt.Sprintf(`set -e
tmp=$(mktemp)
printf '%%s' %s > "$tmp"
if ! sudo cmp -s "$tmp" /etc/systemd/system/%s; then
  sudo install -m 0644 "$tmp" /etc/systemd/system/%s
  sudo systemctl daemon-reload
  sudo systemctl enable %s >/dev/null 2>&1
  sudo systemctl restart %s
else
  sudo systemctl start %s
fi
rm -f "$tmp"`, quote(body), unit, unit, unit, unit, unit)
}

// ComposeConfig prints Compose's resolved config as JSON.
func (s *Scripts) ComposeConfig(f domain.Folder) string {
	return fmt.Sprintf("cd %s && docker compose -f %s config --format json", quote(f.RemotePath), quote(f.Compose))
}

// ClosedPorts prints each port nothing listens on yet.
func (s *Scripts) ClosedPorts(ps []int) string {
	list := make([]string, len(ps))
	for i, p := range ps {
		list[i] = strconv.Itoa(p)
	}
	return fmt.Sprintf(`for p in %s; do timeout 2 bash -c "exec 3<>/dev/tcp/127.0.0.1/$p" 2>/dev/null || echo "$p"; done`, strings.Join(list, " "))
}

// Logs follows a folder's logs (one compose service when named).
func (s *Scripts) Logs(f domain.Folder, envName, service string) string {
	if f.IsCompose() {
		cmd := fmt.Sprintf("cd %s && docker compose -f %s logs -f --tail 200", quote(f.RemotePath), quote(f.Compose))
		if service != "" {
			cmd += " " + quote(service)
		}
		return cmd
	}
	return "sudo journalctl -f -n 200 -u " + quote(UnitName(envName, f))
}

// MemoryPeakGB prints the highest memory use recorded by the idle timer
// (or the current use, before the first check has run).
func (s *Scripts) MemoryPeakGB() string {
	return `kb=$(cat /var/lib/protolift/mem-peak-kb 2>/dev/null || awk '/^MemTotal:/{t=$2} /^MemAvailable:/{a=$2} END{print t-a}' /proc/meminfo); awk -v k="$kb" 'BEGIN{printf "%.1f\n", k/1048576}'`
}

// quote makes s one single-quoted shell word.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// systemdQuote makes s one double-quoted systemd argument: quotes and
// backslashes escaped, $ and % doubled so systemd does not expand them.
func systemdQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `$$`, `%`, `%%`, "\n", " ")
	return `"` + r.Replace(s) + `"`
}

// systemdPath escapes a path for WorkingDirectory=.
func systemdPath(p string) string {
	return strings.NewReplacer(`%`, `%%`, `\`, `\\`, " ", `\x20`).Replace(p)
}
