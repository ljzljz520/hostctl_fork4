package file

import (
	"github.com/guumaster/hostctl/pkg/types"
)

// Toggle alternates between enable and disable status of a types.
// In strict mode the whole batch is validated on the final profile set and
// rolled back atomically when it would produce a conflict.
func (f *File) Toggle(profiles []string) error {
	return f.applyStrict(func() error {
		for _, name := range profiles {
			if name == types.Default {
				continue
			}

			p, ok := f.data.Profiles[name]
			if !ok {
				return types.ErrUnknownProfile
			}

			if p.Status == types.Enabled {
				p.Status = types.Disabled
			} else {
				p.Status = types.Enabled
			}

			f.data.Profiles[name] = p
		}

		return nil
	})
}
