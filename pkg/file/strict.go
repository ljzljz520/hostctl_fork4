package file

import (
	"net"

	"github.com/guumaster/hostctl/pkg/types"
)

// SetStrict enables or disables the strict activation check. When enabled,
// every mutating operation validates the final set of enabled profiles
// before committing it: on a conflict the in-memory content is restored and
// the hosts file is left untouched.
func (f *File) SetStrict(strict bool) {
	f.strict = strict
}

// checkStrict runs the validation only when strict mode is enabled.
func (f *File) checkStrict() error {
	if !f.strict {
		return nil
	}

	return types.ValidateEnabledProfiles(f.data)
}

// applyStrict executes a mutation, making sure that, in strict mode, the
// in-memory content is rolled back whenever the mutation itself or the
// validation of the resulting profile set fails.
func (f *File) applyStrict(mutate func() error) error {
	if !f.strict {
		return mutate()
	}

	backup := cloneContent(f.data)

	if err := mutate(); err != nil {
		f.data = backup

		return err
	}

	if err := types.ValidateEnabledProfiles(f.data); err != nil {
		f.data = backup

		return err
	}

	return nil
}

// cloneContent returns a deep copy of the parsed hosts content so failed
// strict mutations can be rolled back without sharing mutable slices or maps.
func cloneContent(data *types.Content) *types.Content {
	if data == nil {
		return nil
	}

	clone := &types.Content{
		ProfileNames: append([]string(nil), data.ProfileNames...),
		Profiles:     make(map[string]*types.Profile, len(data.Profiles)),
	}

	if data.DefaultProfile != nil {
		clone.DefaultProfile = append(types.DefaultProfile(nil), data.DefaultProfile...)
	}

	for name, p := range data.Profiles {
		if p == nil {
			clone.Profiles[name] = nil

			continue
		}

		np := &types.Profile{
			Name:   p.Name,
			Status: p.Status,
			IPList: append([]string(nil), p.IPList...),
			Routes: make(map[string]*types.Route, len(p.Routes)),
		}

		for ip, r := range p.Routes {
			if r == nil {
				continue
			}

			nr := &types.Route{
				HostNames: append([]string(nil), r.HostNames...),
			}

			if r.IP != nil {
				nr.IP = append(net.IP(nil), r.IP...)
			}

			np.Routes[ip] = nr
		}

		clone.Profiles[name] = np
	}

	return clone
}
