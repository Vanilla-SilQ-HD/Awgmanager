package hydraroute

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrOversizedTagNotFound is returned by RemoveOversizedTag when the tag is
// not in HR Neo's service section.
var ErrOversizedTagNotFound = errors.New("tag is not in the disabled tags list")

// OversizedTag describes a single geoip tag that HR Neo excluded from
// routing because its entry count exceeds IpsetMaxElem. Count is -1 when
// the tag is no longer present in any installed .dat file.
type OversizedTag struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	File  string `json:"file"`
}

// OversizedTags reads the current ip.list, extracts service-block tag
// names, and enriches each with its live entry count from the installed
// geoip .dat files.
func (s *Service) OversizedTags(ctx context.Context) ([]OversizedTag, error) {
	_, names, err := s.ListRules()
	if err != nil {
		return nil, err
	}

	gds := s.GetGeoData()
	result := make([]OversizedTag, 0, len(names))
	for _, full := range names {
		bare := strings.TrimPrefix(full, "geoip:")

		count := -1
		file := ""
		if gds != nil {
			for _, entry := range gds.List() {
				if entry.Type != "geoip" {
					continue
				}
				tags, err := gds.GetTags(entry.Path)
				if err != nil {
					continue
				}
				for _, t := range tags {
					if strings.EqualFold(t.Name, bare) {
						count = t.Count
						file = entry.Path
						break
					}
				}
				if count >= 0 {
					break
				}
			}
		}

		result = append(result, OversizedTag{Name: full, Count: count, File: file})
	}
	return result, nil
}

// RemoveOversizedTag drops a tag from HR Neo's `##impossible to use` section
// in ip.list. HR Neo only appends to the section and does not record which
// rule a tag came from, so it cannot be put back automatically: to route the
// tag again, add it to a rule. Rules stay as they are, so HR Neo is not
// restarted — it ignores the section anyway (`#/` target).
func (s *Service) RemoveOversizedTag(name string) error {
	want := normalizeOversizedTag(name)
	if want == "geoip:" {
		return fmt.Errorf("tag name must not be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.status.Installed {
		return fmt.Errorf("HydraRoute Neo is not installed")
	}
	entries, oversized, err := s.loadEntries()
	if err != nil {
		return err
	}
	kept := make([]string, 0, len(oversized))
	for _, t := range oversized {
		if normalizeOversizedTag(t) != want {
			kept = append(kept, t)
		}
	}
	if len(kept) == len(oversized) {
		return ErrOversizedTagNotFound
	}
	if err := WriteWholeFile(ipListPath, GenerateIPList(sortedEntries(entries), kept)); err != nil {
		return err
	}
	s.appLog.Info("remove-oversized-tag", want, "removed from ip.list service section")
	return nil
}

// normalizeOversizedTag brings a tag to the form HR Neo writes in the
// service section: lower-case, with the `geoip:` prefix.
func normalizeOversizedTag(name string) string {
	t := strings.ToLower(strings.TrimSpace(name))
	return "geoip:" + strings.TrimPrefix(t, "geoip:")
}
