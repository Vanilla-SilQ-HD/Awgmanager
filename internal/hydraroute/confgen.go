package hydraroute

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoaxisr/awg-manager/internal/storage"
)

// Paths are package-level vars so tests can override them via t.TempDir().
var (
	domainConfPath = "/opt/etc/HydraRoute/domain.conf" //nolint:gochecknoglobals
	ipListPath     = "/opt/etc/HydraRoute/ip.list"     //nolint:gochecknoglobals
)

// SetPaths overrides the HR config file paths. Intended for tests that need
// to point the service at a temp directory; returns a restore function.
func SetPaths(domain, ipList string) (restore func()) {
	origDomain, origIP := domainConfPath, ipListPath
	domainConfPath = domain
	ipListPath = ipList
	return func() {
		domainConfPath = origDomain
		ipListPath = origIP
	}
}

// GenerateDomainConf produces the full domain.conf content. We own the
// whole file — no markers, no preserved user blocks. Format per entry:
//
//	## Name
//	domain1,domain2,geosite:TAG/Target
func GenerateDomainConf(lists []ManagedEntry) string {
	var sb strings.Builder
	for _, e := range lists {
		if len(e.Domains) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "## %s\n", e.ListName)
		line := fmt.Sprintf("%s/%s", strings.Join(e.Domains, ","), e.Iface)
		if e.Disabled {
			line = "#" + line
		}
		fmt.Fprintf(&sb, "%s\n", line)
	}
	return sb.String()
}

// GenerateIPList produces the full ip.list content. Format per entry:
//
//	## Name
//	/Target
//	cidr1
//	cidr2
//	<empty line>
//
// A disabled entry gets `#/Target` and keeps its lines as they are — HR Neo's
// own disabled-block format ("содержимое игнорируется"). A `#` in front of a
// CIDR is not a comment for HR Neo: anything but `##`, `#/` and `/` is read
// as an entry, so `#10.0.0.0/8` is an invalid CIDR and HRweb refuses to save
// any rule while the file has one (#1022).
func GenerateIPList(lists []ManagedEntry) string {
	var sb strings.Builder
	for _, e := range lists {
		if len(e.Subnets) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "## %s\n", e.ListName)
		target := fmt.Sprintf("/%s", e.Iface)
		if e.Disabled {
			target = "#" + target
		}
		fmt.Fprintf(&sb, "%s\n", target)
		for _, s := range e.Subnets {
			sb.WriteString(s)
			sb.WriteByte('\n')
		}
		sb.WriteByte('\n') // HR Neo block terminator
	}
	return sb.String()
}

// WriteWholeFile atomically writes content as the entire file body.
// We own the file — anything outside our format is discarded on next write.
func WriteWholeFile(filePath, content string) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return fmt.Errorf("hydraroute: create parent dir: %w", err)
	}
	return atomicWrite(filePath, content)
}

func atomicWrite(filePath, content string) error {
	return storage.AtomicWrite(filePath, []byte(content))
}
