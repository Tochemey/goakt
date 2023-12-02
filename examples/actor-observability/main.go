/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
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

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	goakt "github.com/tochemey/goakt/actors"
	samplepb "github.com/tochemey/goakt/examples/protos/pb/v1"
	"github.com/tochemey/goakt/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.uber.org/atomic"
)

var serviceName = semconv.ServiceNameKey.String("actor-observability")

func initTracer() {
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		panic(err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSyncer(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			serviceName,
		)),
	)

	otel.SetTracerProvider(tp)
}

func initMeter() {
	// The exporter embeds a default OpenTelemetry Reader and
	// implements prometheus.Collector, allowing it to be used as
	// both a Reader and Collector.
	metricExporter, err := prometheus.New()
	if err != nil {
		panic(err)
	}
	meterProvider := metric.NewMeterProvider(
		metric.WithReader(metricExporter),
		metric.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			serviceName,
		)),
	)
	otel.SetMeterProvider(meterProvider)

	http.Handle("/metrics", promhttp.Handler())
	go func() {
		_ = http.ListenAndServe(":2222", nil)
	}()
	fmt.Println("Prometheus server running on :2222")
}

func main() {
	initTracer()
	initMeter()
	ctx := context.Background()

	// use the messages default logger. real-life implement the logger interface`
	logger := log.DefaultLogger
	// create the actor system. kindly in real-life application handle the error
	actorSystem, _ := goakt.NewActorSystem("SampleActorSystem",
		goakt.WithExpireActorAfter(10*time.Second), // set big passivation time
		goakt.WithLogger(logger),
		goakt.WithTracing(),
		goakt.WithActorInitMaxRetries(3))

	// start the actor system
	_ = actorSystem.Start(ctx)

	// create actors
	pinger := NewPingActor()
	ponger := NewPongActor()
	pingerRef, _ := actorSystem.Spawn(ctx, "Ping", pinger)
	pongerRef, _ := actorSystem.Spawn(ctx, "Pong", ponger)

	// start the conversation
	_ = pingerRef.Tell(ctx, pongerRef, new(samplepb.Ping))

	// shutdown both actors after 3 seconds of conversation
	timer := time.AfterFunc(time.Second, func() {
		log.DefaultLogger.Infof("PingActor=%s has processed %d messages", pingerRef.ActorPath().String(), pinger.count.Load())
		log.DefaultLogger.Infof("PongActor=%s has processed %d messages", pongerRef.ActorPath().String(), ponger.count.Load())
		_ = pingerRef.Shutdown(ctx)
		_ = pongerRef.Shutdown(ctx)
	})
	defer timer.Stop()

	// capture ctrl+c
	interruptSignal := make(chan os.Signal, 1)
	signal.Notify(interruptSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interruptSignal

	// stop the actor system
	_ = actorSystem.Stop(ctx)
	os.Exit(0)
}

type PingActor struct {
	count *atomic.Int32
}

var _ goakt.Actor = (*PingActor)(nil)

func NewPingActor() *PingActor {
	return &PingActor{}
}

func (p *PingActor) PreStart(ctx context.Context) error {
	p.count = atomic.NewInt32(0)
	return nil
}

func (p *PingActor) Receive(ctx goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *samplepb.Pong:
		p.count.Inc()
		// reply the sender in case there is a sender
		if ctx.Sender() != goakt.NoSender {
			// let us reply to the sender
			_ = ctx.Self().Tell(ctx.Context(), ctx.Sender(), new(samplepb.Ping))
		}
	default:
		ctx.Unhandled()
	}
}

func (p *PingActor) PostStop(ctx context.Context) error {
	return nil
}

type PongActor struct {
	count *atomic.Int32
}

var _ goakt.Actor = (*PongActor)(nil)

func NewPongActor() *PongActor {
	return &PongActor{}
}

func (p *PongActor) PreStart(ctx context.Context) error {
	p.count = atomic.NewInt32(0)
	return nil
}

func (p *PongActor) Receive(ctx goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *samplepb.Ping:
		p.count.Inc()
		// reply the sender in case there is a sender
		if ctx.Sender() != nil && ctx.Sender() != goakt.NoSender {
			_ = ctx.Self().Tell(ctx.Context(), ctx.Sender(), new(samplepb.Pong))
		}
	default:
		ctx.Unhandled()
	}
}

func (p *PongActor) PostStop(ctx context.Context) error {
	return nil
}
