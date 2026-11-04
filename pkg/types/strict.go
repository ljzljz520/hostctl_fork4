package types

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// ErrStrictConflict is returned when the strict activation check finds
// enabled profiles mapping the same domain to different IPs in the same
// address family.
var ErrStrictConflict = errors.New("strict host conflict")

const (
	familyIPv4 = "IPv4"
	familyIPv6 = "IPv6"
)

// StrictMapping describes one of the addresses a domain is mapped to and
// the enabled profiles that contain that exact mapping.
type StrictMapping struct {
	IP       string
	Profiles []string
}

// StrictConflict describes a domain that enabled profiles map to different
// IPs within a single address family.
type StrictConflict struct {
	Host     string
	Family   string
	Mappings []StrictMapping
}

// StrictConflictsError groups all the conflicts found in a single check.
type StrictConflictsError struct {
	Conflicts []StrictConflict
}

// Is allows errors.Is(err, ErrStrictConflict) to match a StrictConflictsError.
func (e *StrictConflictsError) Is(target error) bool {
	return target == ErrStrictConflict
}

// Error renders a human-readable report with the domain, address family,
// addresses and owning profiles involved in every conflict.
func (e *StrictConflictsError) Error() string {
	var b strings.Builder

	fmt.Fprintf(&b,
		"strict check failed: enabled profiles map the same domain to different IPs in the same address family (hosts file not modified):",
	)

	for _, c := range e.Conflicts {
		fmt.Fprintf(&b, "\n- domain %q (%s):", c.Host, c.Family)

		for _, m := range c.Mappings {
			fmt.Fprintf(&b, "\n    %s from profile(s): %s", m.IP, strings.Join(m.Profiles, ", "))
		}
	}

	return b.String()
}

// hostMapping keeps the distinct addresses a single domain is mapped to in
// one address family, together with the profiles asserting each address.
type hostMapping struct {
	host   string
	family string
	ips    []string
	byIP   map[string][]string
}

// ValidateEnabledProfiles runs the strict activation check against the
// profiles that would be active on the hosts file.
//
// A conflict exists when the same domain is mapped to two different IPs of
// the same address family by profiles that do not agree on the whole set of
// addresses. This means:
//   - the same mapping repeated on several profiles is allowed,
//   - normal IPv4/IPv6 pairs are allowed (each family is checked separately),
//   - a single profile intentionally listing several addresses for a domain
//     (or several profiles listing the exact same multi-address set) is
//     allowed,
//   - disabled profiles and the unmanaged "default" content are ignored.
func ValidateEnabledProfiles(data *Content) error {
	if data == nil {
		return nil
	}

	mappings := map[string]*hostMapping{}
	order := []string{}

	for _, name := range data.ProfileNames {
		if name == Default {
			continue
		}

		p, ok := data.Profiles[name]
		if !ok || p.Status != Enabled {
			continue
		}

		for _, ip := range p.IPList {
			route := p.Routes[ip]
			if route == nil {
				continue
			}

			family := ipFamily(ip)
			if family == "" {
				continue
			}

			for _, host := range route.HostNames {
				key := host + "\x00" + family

				m, ok := mappings[key]
				if !ok {
					m = &hostMapping{
						host:   host,
						family: family,
						byIP:   map[string][]string{},
					}
					mappings[key] = m
					order = append(order, key)
				}

				if _, exists := m.byIP[ip]; !exists {
					m.ips = append(m.ips, ip)
				}

				m.byIP[ip] = appendUnique(m.byIP[ip], name)
			}
		}
	}

	conflicts := []StrictConflict{}

	for _, key := range order {
		m := mappings[key]
		if len(m.ips) < 2 {
			continue
		}

		if sameProfileSets(m.byIP, m.ips) {
			continue
		}

		c := StrictConflict{
			Host:   m.host,
			Family: m.family,
		}

		for _, ip := range m.ips {
			c.Mappings = append(c.Mappings, StrictMapping{
				IP:       ip,
				Profiles: m.byIP[ip],
			})
		}

		conflicts = append(conflicts, c)
	}

	if len(conflicts) == 0 {
		return nil
	}

	return &StrictConflictsError{Conflicts: conflicts}
}

// ipFamily returns "IPv4" or "IPv6" for a valid IP, an empty string otherwise.
func ipFamily(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ""
	}

	if parsed.To4() != nil {
		return familyIPv4
	}

	return familyIPv6
}

// sameProfileSets tells whether every address of the mapping is asserted by
// exactly the same set of profiles. When that is true the profiles agree on
// the address set (duplicated sources or intentional multi-address usage) and
// there is no conflict.
func sameProfileSets(byIP map[string][]string, ips []string) bool {
	reference := sliceToSet(byIP[ips[0]])

	for _, ip := range ips[1:] {
		profiles := byIP[ip]
		if len(profiles) != len(reference) {
			return false
		}

		for _, name := range profiles {
			if !reference[name] {
				return false
			}
		}
	}

	return true
}

func appendUnique(list []string, value string) []string {
	for _, v := range list {
		if v == value {
			return list
		}
	}

	return append(list, value)
}

func sliceToSet(list []string) map[string]bool {
	set := make(map[string]bool, len(list))

	for _, v := range list {
		set[v] = true
	}

	return set
}
