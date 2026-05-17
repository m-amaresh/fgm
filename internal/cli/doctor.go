package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/m-amaresh/fgm/internal/fgm"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check fgm installation health",
		RunE: func(cmd *cobra.Command, _ []string) error {
			manager, err := getManager(cmd)
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			for _, check := range manager.Doctor() {
				fmt.Fprintf(w, "%s %-18s %s\n", doctorStatus(check.Status), check.Name, check.Message)
			}
			return nil
		},
	}
}

func doctorStatus(status string) string {
	switch status {
	case fgm.DoctorOK:
		return green("OK")
	case fgm.DoctorWarn:
		return yellow("WARN")
	case fgm.DoctorFail:
		return red("FAIL")
	default:
		return status
	}
}
