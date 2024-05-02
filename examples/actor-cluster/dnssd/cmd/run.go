/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"

	goakt "github.com/tochemey/goakt/actors"
	"github.com/tochemey/goakt/discovery/dnssd"
	"github.com/tochemey/goakt/examples/actor-cluster/dnssd/service"
	"github.com/tochemey/goakt/log"
)

func initTracer(ctx context.Context, res *resource.Resource, traceURL string) {
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint(traceURL),
	)
	if err != nil {
		panic(err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(sdktrace.NewBatchSpanProcessor(exporter)),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
}

func initMeter(res *resource.Resource) {
	// The exporter embeds a default OpenTelemetry Reader and
	// implements prometheus.Collector, allowing it to be used as
	// both a Reader and Collector.
	metricExporter, err := prometheus.New()
	if err != nil {
		panic(err)
	}
	meterProvider := metric.NewMeterProvider(
		metric.WithReader(metricExporter),
		metric.WithResource(res),
	)
	otel.SetMeterProvider(meterProvider)

	http.Handle("/metrics", promhttp.Handler())
	go func() {
		_ = http.ListenAndServe(":2222", nil)
	}()
	fmt.Println("Prometheus server running on :2222")
}

// runCmd represents the run command
var runCmd = &cobra.Command{
	Use:   "run",
	Short: "A brief description of your command",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		// create a background context
		ctx := context.Background()
		// get the configuration from the env vars
		config, err := service.GetConfig()
		//  handle the error
		if err != nil {
			panic(err)
		}
		// use the address default log. real-life implement the log interface`
		logger := log.New(log.DebugLevel, os.Stdout)

		res, err := resource.New(ctx,
			resource.WithHost(),
			resource.WithProcess(),
			resource.WithTelemetrySDK(),
			resource.WithAttributes(
				semconv.ServiceNameKey.String("accounts"),
			),
		)
		if err != nil {
			panic(err)
		}

		// initialize traces and metric providers
		initTracer(ctx, res, config.TraceURL)
		initMeter(res)
		// define the discovery options
		discoConfig := dnssd.Config{
			DomainName: config.ServiceName,
		}
		// instantiate the dnssd discovery provider
		disco := dnssd.NewDiscovery(&discoConfig)

		// grab the the host
		host, _ := os.Hostname()

		// create the actor system
		actorSystem, err := goakt.NewActorSystem(
			config.ActorSystemName,
			goakt.WithPassivationDisabled(), // set big passivation time
			goakt.WithLogger(logger),
			goakt.WithActorInitMaxRetries(3),
			goakt.WithTracing(),
			goakt.WithRemoting(host, int32(config.RemotingPort)),
			goakt.WithClustering(disco, 20, config.GossipPort, config.ClusterPort))
		// handle the error
		if err != nil {
			logger.Panic(err)
		}

		// start the actor system
		if err := actorSystem.Start(ctx); err != nil {
			logger.Panic(err)
		}

		// create the account service
		accountService := service.NewAccountService(actorSystem, logger, config.Port)
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
