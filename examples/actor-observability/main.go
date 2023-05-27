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
	semconv "go.opentelemetry.io/otel/semconv/v1.18.0"
	"go.uber.org/atomic"
)

var serviceName = semconv.ServiceNameKey.String("actor-observability")

func initTracer() {
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		log.Fatal(err)
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
		log.Fatal(err)
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
	fmt.Println("Prometheus service running on :2222")
}

func main() {
	initTracer()
	initMeter()
	ctx := context.Background()

	// use the messages default log. real-life implement the log interface`
	logger := log.DefaultLogger

	// create the actor system. kindly in real-life application handle the error
	actorSystem, _ := goakt.NewActorSystem("SampleActorSystem",
		goakt.WithExpireActorAfter(10*time.Second), // set big passivation time
		goakt.WithLogger(logger),
		goakt.WithActorInitMaxRetries(3))

	// start the actor system
	_ = actorSystem.Start(ctx)

	// create an actor
	pingActor := actorSystem.StartActor(ctx, "Ping", NewPingActor())
	pongActor := actorSystem.StartActor(ctx, "Pong", NewPongActor())

	// start the conversation
	_ = pingActor.Tell(ctx, pongActor, new(samplepb.Ping))

	// shutdown both actors after 3 seconds of conversation
	timer := time.AfterFunc(3*time.Second, func() {
		logger.Infof("PingActor=%s has processed %d messages", pingActor.ActorPath().String(), pingActor.ReceivedCount(ctx))
		logger.Infof("PongActor=%s has processed %d messages", pongActor.ActorPath().String(), pongActor.ReceivedCount(ctx))
		_ = pingActor.Shutdown(ctx)
		_ = pongActor.Shutdown(ctx)
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
	count  *atomic.Int32
	logger log.Logger
}

var _ goakt.Actor = (*PingActor)(nil)

func NewPingActor() *PingActor {
	return &PingActor{}
}

func (p *PingActor) PreStart(ctx context.Context) error {
	// set the log
	p.logger = log.DefaultLogger
	p.count = atomic.NewInt32(0)
	p.logger.Info("PingActor is about to Start")
	return nil
}

func (p *PingActor) Receive(ctx goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *samplepb.Pong:
		p.logger.Infof(fmt.Sprintf("received Pong from %s", ctx.Sender().ActorPath().String()))
		// reply the sender in case there is a sender
		if ctx.Sender() != goakt.NoSender {
			// let us reply to the sender
			_ = ctx.Self().Tell(ctx.Context(), ctx.Sender(), new(samplepb.Ping))
		}
		p.count.Add(1)
	default:
		p.logger.Panic(goakt.ErrUnhandled)
	}
}

func (p *PingActor) PostStop(ctx context.Context) error {
	p.logger.Info("PingActor is about to stop")
	p.logger.Infof("PingActor has processed=%d messages", p.count.Load())
	return nil
}

type PongActor struct {
	count  *atomic.Int32
	logger log.Logger
}

var _ goakt.Actor = (*PongActor)(nil)

func NewPongActor() *PongActor {
	return &PongActor{}
}

func (p *PongActor) PreStart(ctx context.Context) error {
	// set the log
	p.logger = log.DefaultLogger
	p.count = atomic.NewInt32(0)
	p.logger.Info("PongActor is about to Start")
	return nil
}

func (p *PongActor) Receive(ctx goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *samplepb.Ping:
		p.logger.Infof(fmt.Sprintf("received Ping from %s", ctx.Sender().ActorPath().String()))
		// reply the sender in case there is a sender
		if ctx.Sender() != nil && ctx.Sender() != goakt.NoSender {
			_ = ctx.Self().Tell(ctx.Context(), ctx.Sender(), new(samplepb.Pong))
		}
		p.count.Add(1)
	default:
		p.logger.Panic(goakt.ErrUnhandled)
	}
}

func (p *PongActor) PostStop(ctx context.Context) error {
	p.logger.Info("PongActor is about to stop")
	p.logger.Infof("PongActor has processed=%d messages", p.count.Load())
	return nil
}
