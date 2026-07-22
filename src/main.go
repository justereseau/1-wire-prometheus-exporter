package main

import (
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/promslog"
)

// Define constants
const (
	exporter_name         = "onewire"
	metricsPath           = "/metrics"
	exporter_display_name = "1-Wire Exporter"
)

// Define parameters
var (
	listenAddress = flag.String("web.listen-address", ":9100", "Address to listen on for web interface and telemetry.")
	logLevel      = flag.String("log.level", "info", "Only log messages with the given severity or above. One of: [debug, info, warn, error]")
	sensorPath    = flag.String("devices.path", "/sys/bus/w1/devices/", "Path to the sensor file")
)

var ErrReadSensor = errors.New("failed to read sensor temperature")

// Define Metrics
var (
	Temperature = prometheus.NewDesc(
		prometheus.BuildFQName(exporter_name, "temperature", "celcius"),
		"Temperature of the sensor.",
		[]string{"sensor_id", "sensor_name"},
		nil,
	)
)

func main() {
	// Get parameters
	flag.Parse()

	allowedLogLevel := promslog.NewLevel()
	if err := allowedLogLevel.Set(*logLevel); err != nil {
		promslog.New(&promslog.Config{}).Error("Invalid log level.", "level", *logLevel, "err", err)
		os.Exit(1)
	}
	logger := promslog.New(&promslog.Config{Level: allowedLogLevel})

	logger.Info("Starting " + exporter_name + "_exporter.")

	prometheus.MustRegister(version.NewCollector(exporter_name + "_exporter"))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
			<head><title>RedFish Exporter</title></head>
			<body>
			<h1>RedFish Exporter</h1>
			<p><a href='/metrics'>Metrics</a></p>
			</body>
			</html>`))
	})

	http.HandleFunc(metricsPath, func(w http.ResponseWriter, r *http.Request) {
		handler(w, r, logger)
	})

	logger.Info("Starting to listen.", "address", *listenAddress)
	if err := http.ListenAndServe(*listenAddress, nil); err != nil {
		logger.Error("Failed to start http server.", "err", err)
	}
}

// HTTP handler for the exporter.
func handler(w http.ResponseWriter, request *http.Request, logger *slog.Logger) {
	start := time.Now()

	logger.Debug("Starting scrape")

	// Create a new registry for each scrape and register the collector with it.
	registry := prometheus.NewRegistry()
	registry.MustRegister(&collector{logger: logger, response: w})

	// Delegate http serving to Prometheus client library, which will call collector.Collect.
	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	h.ServeHTTP(w, request)

	logger.Debug("Scrape done.", "duration", time.Since(start).Seconds())
}

// Collector is the interface a collector has to implement.
type collector struct {
	logger   *slog.Logger
	response http.ResponseWriter
}

// Describe implements Prometheus.Collector and sends the descriptors of each metric
func (c collector) Describe(ch chan<- *prometheus.Desc) {}

// Collect implements Prometheus.Collector.
func (c collector) Collect(ch chan<- prometheus.Metric) {
	waitAfterMetrics := sync.WaitGroup{}
	defer waitAfterMetrics.Wait()

	data, err := os.ReadFile(*sensorPath + "w1_bus_master1/w1_master_slaves")
	if err != nil {
		c.logger.Error("Failed to read sensor file", "err", err)
		return
	}

	sensors := strings.Split(string(data), "\n")
	if len(sensors) > 0 {
		sensors = sensors[:len(sensors)-1]
	}

	for _, sensor := range sensors {
		waitAfterMetrics.Add(1)
		go func(sensor string, ch chan<- prometheus.Metric, waitAfterMetrics *sync.WaitGroup) {
			defer waitAfterMetrics.Done()

			c.logger.Debug("Reading sensor", "sensor", sensor)
			sensorData, err := os.ReadFile(*sensorPath + sensor + "/temperature")
			if err != nil {
				c.logger.Error("Failed to read sensor file", "err", err)
				return
			}

			temperature, err := strconv.ParseFloat(string(sensorData)[0:len(string(sensorData))-1], 64)
			if err != nil {
				c.logger.Error("Failed to parse sensor temperature", "err", err)
				return
			}

			ch <- prometheus.MustNewConstMetric(Temperature, prometheus.GaugeValue, temperature/1000.0, sensor, sensor)
		}(sensor, ch, &waitAfterMetrics)
	}
}
