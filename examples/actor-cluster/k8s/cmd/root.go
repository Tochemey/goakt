package cmd

import (
	"os"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/tochemey/goakt/log"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "k8s",
	Short: "GoAkt cluster example",
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	err := godotenv.Load()
	if err != nil && !os.IsNotExist(err) {
		log.Fatalf("Error loading .env file, %s", err.Error())
	}
}
