package actions

import (
	"github.com/spf13/cobra"
)

func newToggleCmd() *cobra.Command {
	toggleCmd := &cobra.Command{
		Use:   "toggle",
		Short: "Change status of a profile on your hosts file.",
		Long: `
Alternates between on/off status of an existing profile.
`,
		Args: commonCheckProfileOnly,
		RunE: func(cmd *cobra.Command, profiles []string) error {
			h, err := openHostFile(cmd)
			if err != nil {
				return err
			}

			err = h.Toggle(profiles)
			if err != nil {
				return err
			}

			return h.Flush()
		},
	}

	toggleCmd.PostRunE = func(cmd *cobra.Command, args []string) error {
		return postActionCmd(cmd, args, toggleCmd, true)
	}

	return toggleCmd
}
