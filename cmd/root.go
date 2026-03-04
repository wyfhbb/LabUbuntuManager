package cmd

import "github.com/spf13/cobra"

var rootCmd = &cobra.Command{
	Use:   "server-mgr",
	Short: "Ubuntu server management CLI",
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}
