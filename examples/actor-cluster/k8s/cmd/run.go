package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	goakt "github.com/tochemey/goakt/actors"
	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/discovery/kubernetes"
	"github.com/tochemey/goakt/examples/actor-cluster/k8s/service"
	"github.com/tochemey/goakt/log"
)

const (
	remotingPort       = 9000
	accountServicePort = 50051

	namespace       = "default"
	applicationName = "accounts"
	actorSystemName = "AccountsSystem"
)

// runCmd represents the run command
var runCmd = &cobra.Command{
	Use:   "run",
	Short: "A brief description of your command",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		// create a background context
		ctx := context.Background()
		// use the messages default log. real-life implement the log interface`
		logger := log.New(log.DebugLevel, os.Stdout)

		// create the k8 configuration
		disco := kubernetes.NewDiscovery()
		// define the discovery options
		discoOptions := discovery.Meta{
			kubernetes.ApplicationName: applicationName,
			kubernetes.ActorSystemName: actorSystemName,
			kubernetes.Namespace:       namespace,
		}

		// create the actor system
		actorSystem, err := goakt.NewActorSystem(
			actorSystemName,
			goakt.WithPassivationDisabled(), // set big passivation time
			goakt.WithLogger(logger),
			goakt.WithActorInitMaxRetries(3),
			goakt.WithClustering(disco, remotingPort, 20, discoOptions))
		// handle the error
		if err != nil {
			logger.Panic(err)
		}

		// start the actor system
		if err := actorSystem.Start(ctx); err != nil {
			logger.Panic(err)
		}

		// create the account service
		accountService := service.NewAccountService(actorSystem, logger, accountServicePort)
		// start the account service
		accountService.Start()

		// wait for interruption/termination
		sigs := make(chan os.Signal, 1)
		done := make(chan struct{}, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		// wait for a shutdown signal, and then shutdown
		go func() {
			<-sigs
			// stop the actor system
			if err := actorSystem.Stop(ctx); err != nil {
				logger.Panic(err)
			}

			// stop the account service
			newCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if err := accountService.Stop(newCtx); err != nil {
				logger.Panic(err)
			}

			done <- struct{}{}
		}()
		<-done
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
}
