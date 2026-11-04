package file

import (
	"errors"

	"github.com/guumaster/hostctl/pkg/types"
)

// ReplaceProfile removes previous profile with same name and add new profile to the list.
// In strict mode the replacement is validated as a whole and rolled back if the
// resulting set of enabled profiles contains a conflict.
func (f *File) ReplaceProfile(p *types.Profile) error {
	return f.applyStrict(func() error {
		err := f.RemoveProfile(p.Name)
		if err != nil && !errors.Is(err, types.ErrUnknownProfile) {
			return err
		}

		return f.AddProfile(p)
	})
}
