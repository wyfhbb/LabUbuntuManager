package main

import (
	"github.com/spf13/cobra"
	"os"
)

var rootCmd = &cobra.Command{
	Use:   "server-mgr",
	Short: "Ubuntu 服务器管理工具",
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}