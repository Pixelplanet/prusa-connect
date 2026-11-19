package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/extension"
	"github.com/prusa3d/prusa-connect/sdk/fault"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
	"github.com/prusa3d/prusa-connect/sdk/wire"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

const (
	name    = "tasmota-power"
	logName = "tasmota-power-control"
	release = extension.Version("0.2.3")

	defaultMQTTPort     = 1883
	defaultMQTTTLSPort  = 8883
	defaultMQTTClientID = "prusa-connect-tasmota"
	defaultCooldownSec  = 120
	configsFileName     = "configs.json"
	settingsFileName    = "settings.json"
	usageFileName       = "usage.json"
	maxPowerSamples     = 360
	maxPrintHistory     = 10
)

//go:embed openapi.json
var openAPI []byte

// Settings holds structured MQTT broker and default auto-off settings.
// Persisted in settings.json under the module's granted data directory.
type Settings struct {
	MQTTHost           string `json:"mqtt_host"`
	MQTTPort           int    `json:"mqtt_port"`
	MQTTTLS            bool   `json:"mqtt_tls"`
	MQTTUsername       string `json:"mqtt_username"`
	MQTTPassword       string `json:"mqtt_password,omitempty"`
	HasPassword        bool   `json:"has_password"`
	KeepPassword       bool   `json:"keep_password,omitempty"`
	MQTTClientID       string `json:"mqtt_client_id"`
	AutoOffCooldownSec int    `json:"auto_off_cooldown_sec"`
	ConfirmTurnOff     *bool  `json:"confirm_turn_off"`

	// Legacy option from 0.1.0; parsed into MQTTHost/MQTTPort/MQTTTLS when present.
	MQTTBroker string `json:"mqtt_broker,omitempty"`
}

func boolPtr(v bool) *bool { return &v }

func (s Settings) confirmTurnOffEnabled() bool {
	if s.ConfirmTurnOff == nil {
		return true
	}
	return *s.ConfirmTurnOff
}

func defaultSettings() Settings {
	return Settings{
		MQTTPort:           defaultMQTTPort,
		MQTTClientID:       defaultMQTTClientID,
		AutoOffCooldownSec: defaultCooldownSec,
		ConfirmTurnOff:     boolPtr(true),
	}
}

func normalizeSettings(s Settings) Settings {
	if strings.TrimSpace(s.MQTTHost) == "" && strings.TrimSpace(s.MQTTBroker) != "" {
		host, port, useTLS := parseBrokerURL(s.MQTTBroker)
		s.MQTTHost = host
		if port > 0 {
			s.MQTTPort = port
		}
		s.MQTTTLS = useTLS
	} else if strings.Contains(s.MQTTHost, "://") || strings.Contains(s.MQTTHost, ":") {
		// If the user pasted a scheme or host:port into the host box, split it cleanly.
		host, port, useTLS := parseBrokerURL(s.MQTTHost)
		if host != "" {
			s.MQTTHost = host
			if port > 0 && s.MQTTPort == 0 {
				s.MQTTPort = port
			}
			if useTLS {
				s.MQTTTLS = true
			}
		}
	}
	s.MQTTHost = strings.TrimSpace(s.MQTTHost)
	s.MQTTBroker = ""
	if s.MQTTPort <= 0 {
		if s.MQTTTLS {
			s.MQTTPort = defaultMQTTTLSPort
		} else {
			s.MQTTPort = defaultMQTTPort
		}
	}
	if strings.TrimSpace(s.MQTTClientID) == "" {
		s.MQTTClientID = defaultMQTTClientID
	} else {
		s.MQTTClientID = strings.TrimSpace(s.MQTTClientID)
	}
	if s.AutoOffCooldownSec < 0 {
		s.AutoOffCooldownSec = 0
	}
	if s.ConfirmTurnOff == nil {
		s.ConfirmTurnOff = boolPtr(true)
	}
	s.HasPassword = s.MQTTPassword != ""
	return s
}

func parseBrokerURL(raw string) (host string, port int, useTLS bool) {
	b := strings.TrimSpace(raw)
	if b == "" {
		return "", 0, false
	}
	if !strings.Contains(b, "://") {
		b = "tcp://" + b
	}
	u, err := url.Parse(b)
	if err != nil {
		return strings.TrimSpace(raw), 0, false
	}
	scheme := strings.ToLower(u.Scheme)
	useTLS = scheme == "ssl" || scheme == "tls" || scheme == "mqtts"
	h := u.Hostname()
	if pStr := u.Port(); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
			port = p
		}
	}
	return h, port, useTLS
}

func (s Settings) brokerURL() string {
	host := strings.TrimSpace(s.MQTTHost)
	if host == "" {
		return ""
	}
	scheme := "tcp"
	if s.MQTTTLS {
		scheme = "ssl"
	}
	port := s.MQTTPort
	if port <= 0 {
		if s.MQTTTLS {
			port = defaultMQTTTLSPort
		} else {
			port = defaultMQTTPort
		}
	}
	return fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(host, strconv.Itoa(port)))
}

func (s Settings) redacted() Settings {
	out := s
	out.HasPassword = s.MQTTPassword != ""
	out.MQTTPassword = ""
	out.KeepPassword = false
	out.MQTTBroker = ""
	return out
}

// DeviceConfig is persisted per printer in configs.json.
type DeviceConfig struct {
	TasmotaTopic      string `json:"tasmota_topic"`
	AutoOffAfterPrint bool   `json:"auto_off_after_print"`
	CooldownSec       *int   `json:"cooldown_sec,omitempty"`
}

// PlugTelemetry holds live MQTT state for a Tasmota topic.
type PlugTelemetry struct {
	Topic        string     `json:"topic,omitempty"`
	FriendlyName string     `json:"friendly_name,omitempty"`
	Power        string     `json:"power"`        // "ON", "OFF", or "UNKNOWN"
	Online       *bool      `json:"online"`       // from tele/<topic>/LWT
	PowerWatts   *float64   `json:"power_watts"`  // from tele/<topic>/SENSOR ENERGY.Power
	Voltage      *float64   `json:"voltage"`      // from tele/<topic>/SENSOR ENERGY.Voltage
	CurrentAmps  *float64   `json:"current_amps"` // from tele/<topic>/SENSOR ENERGY.Current
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
}

type autoOffTracker struct {
	sawActive  bool
	finishedAt *time.Time
}

type axisSample struct {
	X           float64
	Y           float64
	Z           float64
	SampledAt   time.Time
	LastMovedAt time.Time
}

// PowerSample is one timestamped power draw point for the usage graph.
type PowerSample struct {
	Timestamp int64   `json:"ts"`
	Watts     float64 `json:"watts"`
}

// PowerSession tracks energy consumed while the printer plug is turned ON.
type PowerSession struct {
	Active      bool       `json:"active"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	DurationSec int        `json:"duration_sec"`
	EnergyWh    float64    `json:"energy_wh"`
	AvgWatts    float64    `json:"avg_watts"`
	PeakWatts   float64    `json:"peak_watts"`
}

// PrintPowerRecord tracks energy consumed for a single print job / file.
type PrintPowerRecord struct {
	JobID       string     `json:"job_id,omitempty"`
	FileName    string     `json:"file_name"`
	Active      bool       `json:"active"`
	Outcome     string     `json:"outcome,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	DurationSec int        `json:"duration_sec"`
	EnergyWh    float64    `json:"energy_wh"`
	AvgWatts    float64    `json:"avg_watts"`
	PeakWatts   float64    `json:"peak_watts"`
}

// DeviceUsage holds persisted and live power usage metrics for one printer.
type DeviceUsage struct {
	History      []PowerSample      `json:"history,omitempty"`
	Session      *PowerSession      `json:"session,omitempty"`
	CurrentPrint *PrintPowerRecord  `json:"current_print,omitempty"`
	PrintHistory []PrintPowerRecord `json:"print_history,omitempty"`
	LastSampleAt time.Time          `json:"last_sample_at,omitempty"`
}

type module struct {
	devices *deviceapi.Client

	held atomic.Pointer[Settings]

	mu             sync.RWMutex
	dataDir        string
	mqttClient     mqtt.Client
	mqttUp         bool
	mqttBroker     string
	mqttError      string
	plugs          map[string]*PlugTelemetry
	configs        map[string]DeviceConfig
	trackers       map[string]*autoOffTracker
	axes           map[string]*axisSample
	usage          map[string]*DeviceUsage
	lastUsageSaved time.Time
}

func newModule(conn *wirerpc.Conn) *module {
	m := &module{
		devices:  deviceapi.NewClient(conn),
		plugs:    make(map[string]*PlugTelemetry),
		configs:  make(map[string]DeviceConfig),
		trackers: make(map[string]*autoOffTracker),
		axes:     make(map[string]*axisSample),
		usage:    make(map[string]*DeviceUsage),
	}
	def := defaultSettings()
	m.held.Store(&def)
	return m
}

func (m *module) settings() Settings {
	if s := m.held.Load(); s != nil {
		return *s
	}
	return defaultSettings()
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", logName, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	line, err := wire.Handshake{
		Protocol: wire.ProtocolMajor,
		MinMinor: wire.MinMinor,
		MaxMinor: wire.MaxMinor,
		Address:  wire.StdioAddress,
	}.Line()
	if err != nil {
		return fmt.Errorf("render handshake: %w", err)
	}
	if _, err = os.Stdout.WriteString(line); err != nil {
		return fmt.Errorf("write handshake: %w", err)
	}

	var mod *module
	conn, err := wirerpc.NewConn(stdio{}, wirerpc.Options{
		Role: wirerpc.RoleChild,
		Methods: wirerpc.Methods{
			wirerpc.MethodInfo: func(context.Context, wirerpc.Call) (any, error) {
				return routesapi.Describe(wirerpc.Info{
					Name:     name,
					Version:  release,
					Contract: wirerpc.Contract,
					Serves:   []wirerpc.Method{wirerpc.MethodConfigure},
				}, openAPI)
			},
			wirerpc.MethodConfigure: wirerpc.ConfigureGranted(func(_ context.Context, sent Settings, grants wirerpc.Grants) error {
				if grants.Data != "" {
					mod.setDataDir(grants.Data)
					mod.loadAllConfigs()
					mod.loadAllUsage()
				}

				effective := normalizeSettings(sent)
				if stored, ok := mod.loadSettingsFromDisk(); ok {
					effective = stored
				} else if effective.MQTTHost != "" {
					// Migrate legacy host-passed settings into settings.json once.
					_ = mod.writeSettingsToDisk(effective)
				}

				mod.held.Store(&effective)
				mod.reconnectMQTT(effective)
				return nil
			}),
			wirerpc.MethodShutdown: func(context.Context, wirerpc.Call) (any, error) {
				mod.disconnectMQTT()
				return nil, nil
			},
			routesapi.MethodServe: routesapi.Serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mod.routes().ServeHTTP(w, r)
			})),
		},
	})
	if err != nil {
		return fmt.Errorf("build wirerpc session: %w", err)
	}

	mod = newModule(conn)

	// Start background auto-off loop once the pipe is serving.
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
		mod.autoOffLoop(ctx)
	}()

	fmt.Fprintf(os.Stderr, "%s %s serving on stdio (pid %d)\n", logName, release, os.Getpid())
	if err := conn.Serve(ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// --- Persistence (in grants.Data) ---

func (m *module) setDataDir(dir string) {
	m.mu.Lock()
	m.dataDir = dir
	m.mu.Unlock()
}

func (m *module) loadSettingsFromDisk() (Settings, bool) {
	m.mu.RLock()
	dir := m.dataDir
	m.mu.RUnlock()
	if dir == "" {
		return Settings{}, false
	}

	raw, err := os.ReadFile(filepath.Join(dir, settingsFileName))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "%s: read settings: %v\n", logName, err)
		}
		return Settings{}, false
	}

	var loaded Settings
	if err := json.Unmarshal(raw, &loaded); err != nil {
		fmt.Fprintf(os.Stderr, "%s: parse settings: %v\n", logName, err)
		return Settings{}, false
	}
	return normalizeSettings(loaded), true
}

func (m *module) writeSettingsToDisk(s Settings) error {
	m.mu.RLock()
	dir := m.dataDir
	m.mu.RUnlock()
	if dir == "" {
		return nil
	}

	toWrite := s
	toWrite.HasPassword = false
	toWrite.KeepPassword = false
	toWrite.MQTTBroker = ""

	return writeJSONAtomic(filepath.Join(dir, settingsFileName), toWrite)
}

func writeJSONAtomic(target string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

func (m *module) loadAllConfigs() {
	m.mu.RLock()
	dir := m.dataDir
	m.mu.RUnlock()
	if dir == "" {
		return
	}

	raw, err := os.ReadFile(filepath.Join(dir, configsFileName))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "%s: read configs: %v\n", logName, err)
		}
		return
	}

	var loaded map[string]DeviceConfig
	if err := json.Unmarshal(raw, &loaded); err != nil {
		fmt.Fprintf(os.Stderr, "%s: parse configs: %v\n", logName, err)
		return
	}

	m.mu.Lock()
	for id, cfg := range loaded {
		m.configs[id] = cfg
	}
	m.mu.Unlock()

	for _, cfg := range loaded {
		if cfg.TasmotaTopic != "" {
			m.queryPlugState(cfg.TasmotaTopic)
		}
	}
}

func cloneDeviceUsage(u *DeviceUsage) *DeviceUsage {
	if u == nil {
		return nil
	}
	cp := &DeviceUsage{
		LastSampleAt: u.LastSampleAt,
	}
	if len(u.History) > 0 {
		cp.History = append([]PowerSample(nil), u.History...)
	}
	if u.Session != nil {
		s := *u.Session
		if u.Session.EndedAt != nil {
			t := *u.Session.EndedAt
			s.EndedAt = &t
		}
		cp.Session = &s
	}
	if u.CurrentPrint != nil {
		p := *u.CurrentPrint
		if u.CurrentPrint.EndedAt != nil {
			t := *u.CurrentPrint.EndedAt
			p.EndedAt = &t
		}
		cp.CurrentPrint = &p
	}
	if len(u.PrintHistory) > 0 {
		cp.PrintHistory = make([]PrintPowerRecord, len(u.PrintHistory))
		for i, item := range u.PrintHistory {
			cp.PrintHistory[i] = item
			if item.EndedAt != nil {
				t := *item.EndedAt
				cp.PrintHistory[i].EndedAt = &t
			}
		}
	}
	return cp
}

func (m *module) loadAllUsage() {
	m.mu.RLock()
	dir := m.dataDir
	m.mu.RUnlock()
	if dir == "" {
		return
	}

	raw, err := os.ReadFile(filepath.Join(dir, usageFileName))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "%s: read usage: %v\n", logName, err)
		}
		return
	}

	var loaded map[string]*DeviceUsage
	if err := json.Unmarshal(raw, &loaded); err != nil {
		fmt.Fprintf(os.Stderr, "%s: parse usage: %v\n", logName, err)
		return
	}

	m.mu.Lock()
	for id, u := range loaded {
		if u == nil {
			continue
		}
		if len(u.History) > maxPowerSamples {
			u.History = u.History[len(u.History)-maxPowerSamples:]
		}
		if len(u.PrintHistory) > maxPrintHistory {
			u.PrintHistory = u.PrintHistory[:maxPrintHistory]
		}
		m.usage[id] = u
	}
	m.lastUsageSaved = time.Now()
	m.mu.Unlock()
}

func (m *module) saveAllUsage() error {
	m.mu.Lock()
	dir := m.dataDir
	if dir == "" {
		m.mu.Unlock()
		return nil
	}
	snapshot := make(map[string]*DeviceUsage, len(m.usage))
	for k, u := range m.usage {
		if u != nil {
			snapshot[k] = cloneDeviceUsage(u)
		}
	}
	m.lastUsageSaved = time.Now()
	m.mu.Unlock()

	return writeJSONAtomic(filepath.Join(dir, usageFileName), snapshot)
}

func (m *module) shouldPeriodicSaveUsage(interval time.Duration) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastUsageSaved.IsZero() || time.Since(m.lastUsageSaved) >= interval
}

func (m *module) getConfig(_ context.Context, deviceID string) (DeviceConfig, error) {
	m.mu.RLock()
	cfg := m.configs[deviceID]
	m.mu.RUnlock()
	return cfg, nil
}

func (m *module) saveConfig(_ context.Context, deviceID string, cfg DeviceConfig) error {
	cfg.TasmotaTopic = normalizeTopic(cfg.TasmotaTopic)

	m.mu.Lock()
	m.configs[deviceID] = cfg
	if !cfg.AutoOffAfterPrint {
		delete(m.trackers, deviceID)
	}
	dir := m.dataDir
	snapshot := make(map[string]DeviceConfig, len(m.configs))
	for k, v := range m.configs {
		snapshot[k] = v
	}
	m.mu.Unlock()

	if dir != "" {
		if err := writeJSONAtomic(filepath.Join(dir, configsFileName), snapshot); err != nil {
			return err
		}
	}

	if cfg.TasmotaTopic != "" {
		m.queryPlugState(cfg.TasmotaTopic)
	}
	return nil
}

func normalizeTopic(t string) string {
	t = strings.TrimSpace(t)
	t = strings.Trim(t, "/")
	t = strings.TrimPrefix(t, "cmnd/")
	t = strings.TrimPrefix(t, "stat/")
	t = strings.TrimPrefix(t, "tele/")
	t = strings.TrimSuffix(t, "/Power")
	t = strings.TrimSuffix(t, "/POWER")
	return t
}

// --- MQTT ---

func (m *module) disconnectMQTT() {
	m.mu.Lock()
	c := m.mqttClient
	m.mqttClient = nil
	m.mqttUp = false
	m.mu.Unlock()
	if c != nil && c.IsConnected() {
		c.Disconnect(250)
	}
}

func (m *module) buildMQTTOptions(s Settings, brokerURL, clientID string) *mqtt.ClientOptions {
	opts := mqtt.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID(clientID).
		SetConnectTimeout(5 * time.Second).
		SetAutoReconnect(true).
		SetConnectRetry(false)

	if s.MQTTUsername != "" {
		opts.SetUsername(s.MQTTUsername)
		opts.SetPassword(s.MQTTPassword)
	}
	return opts
}

func (m *module) reconnectMQTT(s Settings) {
	m.disconnectMQTT()
	brokerURL := s.brokerURL()
	m.mu.Lock()
	m.mqttBroker = brokerURL
	m.mqttError = ""
	m.mu.Unlock()

	if brokerURL == "" {
		return
	}

	fmt.Fprintf(os.Stderr, "%s: connecting to MQTT broker %s (client_id=%s)\n", logName, brokerURL, s.MQTTClientID)

	opts := m.buildMQTTOptions(s, brokerURL, s.MQTTClientID)

	opts.SetOnConnectHandler(func(c mqtt.Client) {
		m.mu.Lock()
		m.mqttUp = true
		m.mqttError = ""
		topics := make([]string, 0, len(m.configs))
		for _, cfg := range m.configs {
			if cfg.TasmotaTopic != "" {
				topics = append(topics, cfg.TasmotaTopic)
			}
		}
		m.mu.Unlock()

		c.Subscribe("stat/+/POWER", 0, m.onMQTTMessage)
		c.Subscribe("stat/+/RESULT", 0, m.onMQTTMessage)
		c.Subscribe("stat/+/STATUS8", 0, m.onMQTTMessage)
		c.Subscribe("tele/+/LWT", 0, m.onMQTTMessage)
		c.Subscribe("tele/+/STATE", 0, m.onMQTTMessage)
		c.Subscribe("tele/+/SENSOR", 0, m.onMQTTMessage)
		c.Subscribe("tasmota/discovery/+/config", 0, m.onMQTTMessage)

		c.Publish("cmnd/tasmotas/Power", 0, false, "")
		for _, t := range topics {
			c.Publish("cmnd/"+t+"/Power", 0, false, "")
			c.Publish("cmnd/"+t+"/Status", 0, false, "8")
		}
		fmt.Fprintf(os.Stderr, "%s: connected to MQTT broker %s\n", logName, brokerURL)
	})

	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		m.mu.Lock()
		m.mqttUp = false
		if err != nil {
			m.mqttError = err.Error()
		}
		m.mu.Unlock()
		fmt.Fprintf(os.Stderr, "%s: MQTT connection lost: %v\n", logName, err)
	})

	opts.SetReconnectingHandler(func(_ mqtt.Client, _ *mqtt.ClientOptions) {
		fmt.Fprintf(os.Stderr, "%s: retrying MQTT connection to %s\n", logName, brokerURL)
	})

	client := mqtt.NewClient(opts)
	m.mu.Lock()
	m.mqttClient = client
	m.mu.Unlock()

	go func() {
		for {
			m.mu.RLock()
			current := m.mqttClient
			m.mu.RUnlock()
			if current != client {
				return
			}
			tok := client.Connect()
			if tok.WaitTimeout(8 * time.Second) {
				if err := tok.Error(); err == nil {
					return
				} else {
					m.mu.Lock()
					m.mqttError = err.Error()
					m.mu.Unlock()
					fmt.Fprintf(os.Stderr, "%s: MQTT connect failed (retrying in 10s): %v\n", logName, err)
				}
			} else {
				m.mu.Lock()
				m.mqttError = "connection timed out"
				m.mu.Unlock()
			}
			time.Sleep(10 * time.Second)
		}
	}()
}

func (m *module) testBroker(override *Settings) (bool, string, string) {
	saved := m.settings()
	candidate := saved
	if override != nil {
		candidate = *override
		if candidate.MQTTPassword == "" && candidate.KeepPassword {
			candidate.MQTTPassword = saved.MQTTPassword
		}
		candidate = normalizeSettings(candidate)
	}

	brokerURL := candidate.brokerURL()
	if brokerURL == "" {
		return false, "", "Broker host is required"
	}

	testOpts := mqtt.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID(fmt.Sprintf("%s-test-%d", candidate.MQTTClientID, time.Now().UnixNano()%10000)).
		SetConnectTimeout(5 * time.Second).
		SetAutoReconnect(false).
		SetConnectRetry(false)
	if candidate.MQTTUsername != "" {
		testOpts.SetUsername(candidate.MQTTUsername)
		testOpts.SetPassword(candidate.MQTTPassword)
	}

	tc := mqtt.NewClient(testOpts)
	tok := tc.Connect()
	if !tok.WaitTimeout(6 * time.Second) {
		return false, brokerURL, "connection timed out after 6s"
	}
	if err := tok.Error(); err != nil {
		return false, brokerURL, err.Error()
	}
	tc.Disconnect(100)
	return true, brokerURL, ""
}

func (m *module) discoverPlugs() {
	m.mu.RLock()
	c := m.mqttClient
	up := m.mqttUp
	m.mu.RUnlock()
	if c != nil && up {
		c.Publish("cmnd/tasmotas/Power", 0, false, "")
	}
}

func (m *module) discoveredListLocked() []PlugTelemetry {
	out := make([]PlugTelemetry, 0, len(m.plugs))
	for topic, p := range m.plugs {
		cp := *p
		cp.Topic = topic
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Topic < out[j].Topic
	})
	return out
}

func (m *module) onMQTTMessage(_ mqtt.Client, msg mqtt.Message) {
	parts := strings.Split(msg.Topic(), "/")
	if len(parts) < 3 {
		return
	}
	now := time.Now()

	// Tasmota native discovery: tasmota/discovery/<mac>/config
	if len(parts) == 4 && parts[0] == "tasmota" && parts[1] == "discovery" && parts[3] == "config" {
		var disc struct {
			T  string   `json:"t"`
			FN []string `json:"fn"`
			HN string   `json:"hn"`
		}
		if json.Unmarshal(msg.Payload(), &disc) != nil || disc.T == "" {
			return
		}
		m.mu.Lock()
		plug, ok := m.plugs[disc.T]
		if !ok {
			plug = &PlugTelemetry{Topic: disc.T, Power: "UNKNOWN"}
			m.plugs[disc.T] = plug
		}
		if len(disc.FN) > 0 && disc.FN[0] != "" {
			plug.FriendlyName = disc.FN[0]
		} else if disc.HN != "" && plug.FriendlyName == "" {
			plug.FriendlyName = disc.HN
		}
		plug.UpdatedAt = &now
		m.mu.Unlock()
		return
	}

	prefix := parts[0]
	topic := strings.Join(parts[1:len(parts)-1], "/")
	leaf := parts[len(parts)-1]
	payload := strings.TrimSpace(string(msg.Payload()))

	m.mu.Lock()
	defer m.mu.Unlock()

	plug, ok := m.plugs[topic]
	if !ok {
		plug = &PlugTelemetry{Topic: topic, Power: "UNKNOWN"}
		m.plugs[topic] = plug
	}

	switch {
	case prefix == "stat" && strings.EqualFold(leaf, "POWER"):
		up := strings.ToUpper(payload)
		if up == "ON" || up == "OFF" {
			plug.Power = up
			plug.UpdatedAt = &now
		}
	case (prefix == "stat" && strings.EqualFold(leaf, "RESULT")) || (prefix == "tele" && leaf == "STATE"):
		var st struct {
			POWER string `json:"POWER"`
		}
		if json.Unmarshal(msg.Payload(), &st) == nil && st.POWER != "" {
			up := strings.ToUpper(st.POWER)
			if up == "ON" || up == "OFF" {
				plug.Power = up
				plug.UpdatedAt = &now
			}
		}
	case prefix == "tele" && leaf == "LWT":
		online := strings.EqualFold(payload, "Online")
		plug.Online = &online
		plug.UpdatedAt = &now
	case prefix == "tele" && leaf == "SENSOR":
		var sens struct {
			ENERGY *energyPayload `json:"ENERGY"`
		}
		if json.Unmarshal(msg.Payload(), &sens) == nil && sens.ENERGY != nil {
			applyEnergyReading(plug, sens.ENERGY, now)
		}
	case prefix == "stat" && strings.EqualFold(leaf, "STATUS8"):
		var st8 struct {
			StatusSNS *struct {
				ENERGY *energyPayload `json:"ENERGY"`
			} `json:"StatusSNS"`
		}
		if json.Unmarshal(msg.Payload(), &st8) == nil && st8.StatusSNS != nil && st8.StatusSNS.ENERGY != nil {
			applyEnergyReading(plug, st8.StatusSNS.ENERGY, now)
		}
	}
}

type energyPayload struct {
	Power   *float64 `json:"Power"`
	Voltage *float64 `json:"Voltage"`
	Current *float64 `json:"Current"`
}

func applyEnergyReading(plug *PlugTelemetry, energy *energyPayload, now time.Time) {
	if energy == nil {
		return
	}
	plug.PowerWatts = energy.Power
	plug.Voltage = energy.Voltage
	plug.CurrentAmps = energy.Current
	plug.UpdatedAt = &now
}

func (m *module) queryPlugState(topic string) {
	m.mu.RLock()
	c := m.mqttClient
	up := m.mqttUp
	m.mu.RUnlock()
	if c != nil && up && topic != "" {
		c.Publish("cmnd/"+topic+"/Power", 0, false, "")
		c.Publish("cmnd/"+topic+"/Status", 0, false, "8")
	}
}

func (m *module) pollAssignedPlugsEnergy() {
	m.mu.RLock()
	c := m.mqttClient
	up := m.mqttUp
	if c == nil || !up {
		m.mu.RUnlock()
		return
	}
	seen := make(map[string]struct{})
	var topics []string
	var unknownPower []string
	for _, cfg := range m.configs {
		t := cfg.TasmotaTopic
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		topics = append(topics, t)
		if p := m.plugs[t]; p == nil || p.Power == "" || p.Power == "UNKNOWN" {
			unknownPower = append(unknownPower, t)
		}
	}
	m.mu.RUnlock()

	for _, t := range unknownPower {
		c.Publish("cmnd/"+t+"/Power", 0, false, "")
	}
	for _, t := range topics {
		c.Publish("cmnd/"+t+"/Status", 0, false, "8")
	}
}

func (m *module) publishPower(topic, action string) error {
	m.mu.RLock()
	c := m.mqttClient
	up := m.mqttUp
	m.mu.RUnlock()
	if c == nil || !up {
		return errors.New("mqtt broker not connected")
	}
	tok := c.Publish("cmnd/"+topic+"/Power", 0, false, strings.ToUpper(action))
	tok.WaitTimeout(5 * time.Second)
	return tok.Error()
}

// --- Printer Activity & Safety Interlock ---

type printerSafety struct {
	DeviceName     string
	Connected      bool
	PrinterState   string
	ActiveJob      bool
	Moving         bool
	CanTurnOff     bool
	LockReason     string
	JobID          string
	JobFile        string
	LastJobID      string
	LastJobOutcome string
}

func readStateString(state map[string]json.RawMessage, key string) string {
	raw, ok := state[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

func readStateToken(state map[string]json.RawMessage, key string) string {
	raw, ok := state[key]
	if !ok {
		return ""
	}
	if s := readStateString(state, key); s != "" {
		return s
	}
	trimmed := strings.Trim(strings.TrimSpace(string(raw)), "\"")
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	return trimmed
}

func cleanJobFileName(displayName, filePath, deviceJobID string) string {
	if d := strings.TrimSpace(displayName); d != "" {
		return d
	}
	if p := strings.ReplaceAll(strings.TrimSpace(filePath), "\\", "/"); p != "" {
		base := path.Base(p)
		if base != "" && base != "." && base != "/" {
			return base
		}
	}
	if id := strings.TrimSpace(deviceJobID); id != "" {
		return "Print #" + id
	}
	return ""
}

func readAxisFloat(state map[string]json.RawMessage, path string) (float64, bool) {
	raw, ok := state[path]
	if !ok {
		return 0, false
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, false
	}
	return v, true
}

func (m *module) updateAxisMotion(deviceID string, state map[string]json.RawMessage) bool {
	x, hasX := readAxisFloat(state, "/axis/x")
	y, hasY := readAxisFloat(state, "/axis/y")
	z, hasZ := readAxisFloat(state, "/axis/z")
	if !hasX && !hasY && !hasZ {
		return false
	}

	now := time.Now()
	const moveThreshold = 0.05
	const settleWindow = 8 * time.Second

	m.mu.Lock()
	defer m.mu.Unlock()

	prev, ok := m.axes[deviceID]
	if !ok {
		m.axes[deviceID] = &axisSample{
			X:         x,
			Y:         y,
			Z:         z,
			SampledAt: now,
		}
		return false
	}

	if !prev.SampledAt.IsZero() && now.Sub(prev.SampledAt) < 30*time.Second {
		if (hasX && math.Abs(x-prev.X) > moveThreshold) ||
			(hasY && math.Abs(y-prev.Y) > moveThreshold) ||
			(hasZ && math.Abs(z-prev.Z) > moveThreshold) {
			prev.LastMovedAt = now
		}
	}

	if hasX {
		prev.X = x
	}
	if hasY {
		prev.Y = y
	}
	if hasZ {
		prev.Z = z
	}
	prev.SampledAt = now

	return !prev.LastMovedAt.IsZero() && now.Sub(prev.LastMovedAt) < settleWindow
}

func (m *module) checkSafety(ctx context.Context, principal, deviceID string) (printerSafety, error) {
	dev, err := m.devices.Get(ctx, deviceapi.GetParams{Principal: principal, ID: deviceID})
	if err != nil {
		return printerSafety{}, err
	}

	devName := strings.TrimSpace(dev.Name)
	if devName == "" {
		devName = deviceID
	}

	var stateStr string
	if raw, ok := dev.State["/state"]; ok {
		_ = json.Unmarshal(raw, &stateStr)
	}
	stateUpper := strings.ToUpper(strings.TrimSpace(stateStr))

	jobs, err := m.devices.Jobs(ctx, deviceapi.JobsParams{Principal: principal, ID: deviceID})
	activeJob := err == nil && jobs.Current != nil && jobs.Current.Ended == nil

	var jobID, jobFile, lastJobID, lastJobOutcome string
	if err == nil {
		if jobs.Current != nil && jobs.Current.Ended == nil {
			jobID = strings.TrimSpace(jobs.Current.ID)
			if jobID == "" {
				jobID = strings.TrimSpace(jobs.Current.DeviceJobID)
			}
			jobFile = cleanJobFileName(jobs.Current.DisplayName, jobs.Current.Path, jobs.Current.DeviceJobID)
		}
		if jobs.Last != nil {
			lastJobID = strings.TrimSpace(jobs.Last.ID)
			lastJobOutcome = strings.TrimSpace(jobs.Last.Outcome)
		}
	}
	if jobFile == "" {
		stateDisplay := readStateString(dev.State, "/job/display")
		statePath := readStateString(dev.State, "/job/path")
		stateJobID := readStateToken(dev.State, "/job/id")
		if jobID == "" && stateJobID != "" {
			jobID = stateJobID
		}
		jobFile = cleanJobFileName(stateDisplay, statePath, stateJobID)
	}

	moving := dev.Connected && m.updateAxisMotion(deviceID, dev.State)

	s := printerSafety{
		DeviceName:     devName,
		Connected:      dev.Connected,
		PrinterState:   stateUpper,
		ActiveJob:      activeJob,
		Moving:         moving,
		CanTurnOff:     true,
		JobID:          jobID,
		JobFile:        jobFile,
		LastJobID:      lastJobID,
		LastJobOutcome: lastJobOutcome,
	}

	if !dev.Connected {
		return s, nil
	}

	switch stateUpper {
	case "PRINTING", "PAUSED", "BUSY", "ATTENTION":
		s.CanTurnOff = false
		s.LockReason = fmt.Sprintf("Printer is currently %s", strings.ToLower(stateUpper))
		return s, nil
	}

	if activeJob {
		s.CanTurnOff = false
		s.LockReason = "Printer has an active print job"
		return s, nil
	}

	if moving {
		s.CanTurnOff = false
		s.LockReason = "Printer axes are still moving"
		return s, nil
	}

	return s, nil
}

func accumulateEnergy(now, startedAt time.Time, watts float64, dt time.Duration, integrate bool, durationSec *int, energyWh, avgWatts, peakWatts *float64) {
	if integrate && dt > 0 {
		*energyWh += watts * dt.Hours()
	}
	*durationSec = int(now.Sub(startedAt).Seconds())
	if watts > *peakWatts {
		*peakWatts = watts
	}
	if *durationSec > 0 {
		*avgWatts = *energyWh / (float64(*durationSec) / 3600.0)
	}
}

func (m *module) updateDeviceUsage(deviceID, topic string, safety printerSafety) (dirtyDisk bool, active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p := m.plugs[topic]
	if p == nil {
		return false, false
	}

	plugOn := p.Power == "ON"
	plugOff := p.Power == "OFF"
	if !plugOn && !plugOff {
		return false, false
	}

	watts := 0.0
	if plugOn && p.PowerWatts != nil && *p.PowerWatts >= 0 {
		watts = *p.PowerWatts
	}

	now := time.Now()
	u := m.usage[deviceID]
	if u == nil {
		u = &DeviceUsage{}
		m.usage[deviceID] = u
	}

	sampleDue := u.LastSampleAt.IsZero() || now.Sub(u.LastSampleAt) >= 2*time.Second
	var dt time.Duration
	if sampleDue {
		if !u.LastSampleAt.IsZero() && now.After(u.LastSampleAt) && now.Sub(u.LastSampleAt) <= 2*time.Minute {
			dt = now.Sub(u.LastSampleAt)
		}
		u.LastSampleAt = now
		u.History = append(u.History, PowerSample{
			Timestamp: now.Unix(),
			Watts:     math.Round(watts*10) / 10,
		})
		if len(u.History) > maxPowerSamples {
			u.History = u.History[len(u.History)-maxPowerSamples:]
		}
	}

	if plugOn {
		if u.Session == nil || !u.Session.Active {
			u.Session = &PowerSession{
				Active:    true,
				StartedAt: now,
				PeakWatts: watts,
			}
			dirtyDisk = true
		} else {
			accumulateEnergy(now, u.Session.StartedAt, watts, dt, sampleDue,
				&u.Session.DurationSec, &u.Session.EnergyWh, &u.Session.AvgWatts, &u.Session.PeakWatts)
		}
	} else if plugOff && u.Session != nil && u.Session.Active {
		u.Session.Active = false
		ended := now
		u.Session.EndedAt = &ended
		u.Session.DurationSec = int(now.Sub(u.Session.StartedAt).Seconds())
		if u.Session.DurationSec > 0 {
			u.Session.AvgWatts = u.Session.EnergyWh / (float64(u.Session.DurationSec) / 3600.0)
		}
		dirtyDisk = true
	}

	printingNow := safety.Connected && (safety.ActiveJob || safety.PrinterState == "PRINTING" || safety.PrinterState == "PAUSED")
	finishingMoves := u.CurrentPrint != nil && u.CurrentPrint.Active && safety.Connected && safety.Moving
	printActive := printingNow || finishingMoves

	finalizeCurrentPrint := func(outcome string) {
		if u.CurrentPrint == nil || !u.CurrentPrint.Active {
			return
		}
		u.CurrentPrint.Active = false
		ended := now
		u.CurrentPrint.EndedAt = &ended
		u.CurrentPrint.DurationSec = int(now.Sub(u.CurrentPrint.StartedAt).Seconds())
		if u.CurrentPrint.DurationSec > 0 {
			u.CurrentPrint.AvgWatts = u.CurrentPrint.EnergyWh / (float64(u.CurrentPrint.DurationSec) / 3600.0)
		}
		if outcome == "" {
			if safety.LastJobID != "" && safety.LastJobID == u.CurrentPrint.JobID && safety.LastJobOutcome != "" {
				outcome = safety.LastJobOutcome
			} else if safety.PrinterState == "STOPPED" {
				outcome = "cancelled"
			} else {
				outcome = "finished"
			}
		}
		u.CurrentPrint.Outcome = outcome
		doneCopy := *u.CurrentPrint
		u.PrintHistory = append([]PrintPowerRecord{doneCopy}, u.PrintHistory...)
		if len(u.PrintHistory) > maxPrintHistory {
			u.PrintHistory = u.PrintHistory[:maxPrintHistory]
		}
		dirtyDisk = true
	}

	if printActive {
		if u.CurrentPrint != nil && u.CurrentPrint.Active && safety.JobID != "" && u.CurrentPrint.JobID != "" && u.CurrentPrint.JobID != safety.JobID {
			finalizeCurrentPrint("finished")
		}
		if u.CurrentPrint == nil || !u.CurrentPrint.Active {
			fileName := safety.JobFile
			if fileName == "" {
				fileName = "Active Print"
			}
			u.CurrentPrint = &PrintPowerRecord{
				JobID:     safety.JobID,
				FileName:  fileName,
				Active:    true,
				StartedAt: now,
				PeakWatts: watts,
			}
			dirtyDisk = true
		} else {
			if safety.JobID != "" && u.CurrentPrint.JobID == "" {
				u.CurrentPrint.JobID = safety.JobID
			}
			if safety.JobFile != "" && (u.CurrentPrint.FileName == "" || u.CurrentPrint.FileName == "Active Print") {
				u.CurrentPrint.FileName = safety.JobFile
				dirtyDisk = true
			}
			accumulateEnergy(now, u.CurrentPrint.StartedAt, watts, dt, plugOn && sampleDue,
				&u.CurrentPrint.DurationSec, &u.CurrentPrint.EnergyWh, &u.CurrentPrint.AvgWatts, &u.CurrentPrint.PeakWatts)
		}
	} else if u.CurrentPrint != nil && u.CurrentPrint.Active {
		finalizeCurrentPrint("")
	}

	active = (u.Session != nil && u.Session.Active) || (u.CurrentPrint != nil && u.CurrentPrint.Active)
	return dirtyDisk, active
}

// --- Background Auto-Off & Usage Polling Loop ---

func (m *module) autoOffLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.stepAutoOff(ctx)
		}
	}
}

func (m *module) stepAutoOff(ctx context.Context) {
	m.pollAssignedPlugsEnergy()

	m.mu.RLock()
	assigned := make(map[string]DeviceConfig)
	for id, cfg := range m.configs {
		if cfg.TasmotaTopic != "" {
			assigned[id] = cfg
		}
	}
	m.mu.RUnlock()

	if len(assigned) == 0 {
		return
	}

	defaultCooldown := m.settings().AutoOffCooldownSec
	var anyDirty bool
	var hasActiveUsage bool

	for deviceID, cfg := range assigned {
		safety, err := m.checkSafety(ctx, "", deviceID)
		if err != nil {
			continue
		}

		dirty, active := m.updateDeviceUsage(deviceID, cfg.TasmotaTopic, safety)
		anyDirty = anyDirty || dirty
		hasActiveUsage = hasActiveUsage || active

		if !cfg.AutoOffAfterPrint {
			continue
		}

		cooldownSec := defaultCooldown
		if cfg.CooldownSec != nil {
			cooldownSec = *cfg.CooldownSec
		}

		m.mu.Lock()
		tr, ok := m.trackers[deviceID]
		if !ok {
			tr = &autoOffTracker{}
			m.trackers[deviceID] = tr
		}

		if !safety.CanTurnOff {
			tr.sawActive = true
			tr.finishedAt = nil
			m.mu.Unlock()
			continue
		}

		if !tr.sawActive && safety.PrinterState != "FINISHED" {
			m.mu.Unlock()
			continue
		}

		if tr.finishedAt == nil {
			now := time.Now()
			tr.finishedAt = &now
		}

		elapsed := time.Since(*tr.finishedAt)
		ready := elapsed >= time.Duration(cooldownSec)*time.Second
		m.mu.Unlock()

		if !ready {
			continue
		}

		recheck, err := m.checkSafety(ctx, "", deviceID)
		if err != nil || !recheck.CanTurnOff {
			continue
		}

		if err := m.publishPower(cfg.TasmotaTopic, "OFF"); err != nil {
			fmt.Fprintf(os.Stderr, "%s: auto-off publish failed for printer %q [%s]: %v\n", logName, safety.DeviceName, deviceID, err)
			continue
		}

		fmt.Fprintf(os.Stderr, "%s: auto-off triggered for printer %q [%s] (topic %s)\n", logName, safety.DeviceName, deviceID, cfg.TasmotaTopic)
		cfg.AutoOffAfterPrint = false
		_ = m.saveConfig(ctx, deviceID, cfg)
	}

	if anyDirty || (hasActiveUsage && m.shouldPeriodicSaveUsage(60*time.Second)) {
		if err := m.saveAllUsage(); err != nil {
			fmt.Fprintf(os.Stderr, "%s: save usage: %v\n", logName, err)
		}
	}
}

// --- HTTP Routes ---

type settingsResponse struct {
	Settings       Settings `json:"settings"`
	MQTTConfigured bool     `json:"mqtt_configured"`
	MQTTConnected  bool     `json:"mqtt_connected"`
	MQTTBroker     string   `json:"mqtt_broker,omitempty"`
	MQTTError      string   `json:"mqtt_error,omitempty"`
}

func (m *module) buildSettingsResponse() settingsResponse {
	s := m.settings()
	m.mu.RLock()
	up := m.mqttUp
	broker := m.mqttBroker
	errMsg := m.mqttError
	m.mu.RUnlock()

	return settingsResponse{
		Settings:       s.redacted(),
		MQTTConfigured: strings.TrimSpace(s.MQTTHost) != "",
		MQTTConnected:  up,
		MQTTBroker:     broker,
		MQTTError:      errMsg,
	}
}

type statusResponse struct {
	DeviceID            string          `json:"device_id"`
	MQTTConfigured      bool            `json:"mqtt_configured"`
	MQTTConnected       bool            `json:"mqtt_connected"`
	MQTTBroker          string          `json:"mqtt_broker,omitempty"`
	MQTTError           string          `json:"mqtt_error,omitempty"`
	Config              DeviceConfig    `json:"config"`
	Plug                *PlugTelemetry  `json:"plug,omitempty"`
	DiscoveredPlugs     []PlugTelemetry `json:"discovered_plugs,omitempty"`
	CanTurnOff          bool            `json:"can_turn_off"`
	LockReason          string          `json:"lock_reason,omitempty"`
	PrinterConnected    bool               `json:"printer_connected"`
	PrinterState        string             `json:"printer_state,omitempty"`
	PrinterMoving       bool               `json:"printer_moving"`
	AutoOffPending      bool               `json:"auto_off_pending"`
	AutoOffRemainingSec int                `json:"auto_off_remaining_sec"`
	DefaultCooldownSec  int                `json:"default_cooldown_sec"`
	ConfirmTurnOff      bool               `json:"confirm_turn_off"`
	PowerHistory        []PowerSample      `json:"power_history,omitempty"`
	SessionUsage        *PowerSession      `json:"session_usage,omitempty"`
	CurrentPrintUsage   *PrintPowerRecord  `json:"current_print_usage,omitempty"`
	PrintHistory        []PrintPowerRecord `json:"print_history,omitempty"`
}

func (m *module) buildStatus(ctx context.Context, principal, deviceID string) (statusResponse, error) {
	cfg, err := m.getConfig(ctx, deviceID)
	if err != nil {
		return statusResponse{}, err
	}
	safety, err := m.checkSafety(ctx, principal, deviceID)
	if err != nil {
		return statusResponse{}, err
	}

	if cfg.TasmotaTopic != "" {
		if dirty, _ := m.updateDeviceUsage(deviceID, cfg.TasmotaTopic, safety); dirty {
			_ = m.saveAllUsage()
		}
	}

	s := m.settings()
	cooldownSec := s.AutoOffCooldownSec
	if cfg.CooldownSec != nil {
		cooldownSec = *cfg.CooldownSec
	}

	m.mu.RLock()
	mqttUp := m.mqttUp
	mqttBroker := m.mqttBroker
	mqttErr := m.mqttError
	var plugCopy *PlugTelemetry
	if cfg.TasmotaTopic != "" {
		if p, ok := m.plugs[cfg.TasmotaTopic]; ok {
			cp := *p
			plugCopy = &cp
		} else {
			plugCopy = &PlugTelemetry{Topic: cfg.TasmotaTopic, Power: "UNKNOWN"}
		}
	}
	var pending bool
	var remaining int
	if cfg.AutoOffAfterPrint {
		if tr, ok := m.trackers[deviceID]; ok {
			if tr.finishedAt != nil && safety.CanTurnOff {
				pending = true
				rem := int((time.Duration(cooldownSec)*time.Second - time.Since(*tr.finishedAt)).Seconds())
				if rem > 0 {
					remaining = rem
				}
			} else if tr.sawActive && safety.Moving {
				pending = true
			}
		}
	}
	usageCopy := cloneDeviceUsage(m.usage[deviceID])
	discovered := m.discoveredListLocked()
	m.mu.RUnlock()

	var (
		historyCopy      []PowerSample
		sessionCopy      *PowerSession
		currentPrintCopy *PrintPowerRecord
		printHistoryCopy []PrintPowerRecord
	)
	if usageCopy != nil {
		historyCopy = usageCopy.History
		sessionCopy = usageCopy.Session
		currentPrintCopy = usageCopy.CurrentPrint
		printHistoryCopy = usageCopy.PrintHistory
	}

	return statusResponse{
		DeviceID:            deviceID,
		MQTTConfigured:      strings.TrimSpace(s.MQTTHost) != "",
		MQTTConnected:       mqttUp,
		MQTTBroker:          mqttBroker,
		MQTTError:           mqttErr,
		Config:              cfg,
		Plug:                plugCopy,
		DiscoveredPlugs:     discovered,
		CanTurnOff:          safety.CanTurnOff,
		LockReason:          safety.LockReason,
		PrinterConnected:    safety.Connected,
		PrinterState:        safety.PrinterState,
		PrinterMoving:       safety.Moving,
		AutoOffPending:      pending,
		AutoOffRemainingSec: remaining,
		DefaultCooldownSec:  s.AutoOffCooldownSec,
		ConfirmTurnOff:      s.confirmTurnOffEnabled(),
		PowerHistory:        historyCopy,
		SessionUsage:        sessionCopy,
		CurrentPrintUsage:   currentPrintCopy,
		PrintHistory:        printHistoryCopy,
	}, nil
}

func (m *module) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /settings", func(w http.ResponseWriter, _ *http.Request) {
		answer(w, http.StatusOK, m.buildSettingsResponse())
	})

	mux.HandleFunc("PUT /settings", func(w http.ResponseWriter, r *http.Request) {
		var body Settings
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			refuse(w, r, InvalidInput.New(err.Error()))
			return
		}
		current := m.settings()
		if body.MQTTPassword == "" && body.KeepPassword {
			body.MQTTPassword = current.MQTTPassword
		}
		next := normalizeSettings(body)
		if err := m.writeSettingsToDisk(next); err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		m.held.Store(&next)
		m.reconnectMQTT(next)
		// Allow brief time for initial connect so the returned status reflects it if immediate.
		time.Sleep(300 * time.Millisecond)
		answer(w, http.StatusOK, m.buildSettingsResponse())
	})

	mux.HandleFunc("POST /broker/test", func(w http.ResponseWriter, r *http.Request) {
		var override *Settings
		if r.Body != nil {
			raw, err := io.ReadAll(io.LimitReader(r.Body, 16<<10))
			if err == nil && len(strings.TrimSpace(string(raw))) > 0 {
				var body Settings
				if err := json.Unmarshal(raw, &body); err != nil {
					refuse(w, r, InvalidInput.New(err.Error()))
					return
				}
				override = &body
			}
		}
		ok, broker, errMsg := m.testBroker(override)
		answer(w, http.StatusOK, map[string]any{
			"ok":     ok,
			"broker": broker,
			"error":  errMsg,
		})
	})

	mux.HandleFunc("POST /discover", func(w http.ResponseWriter, _ *http.Request) {
		m.discoverPlugs()
		time.Sleep(400 * time.Millisecond)
		m.mu.RLock()
		discovered := m.discoveredListLocked()
		m.mu.RUnlock()
		answer(w, http.StatusOK, map[string]any{
			"plugs": discovered,
		})
	})

	mux.HandleFunc("GET /assignments", func(w http.ResponseWriter, _ *http.Request) {
		s := m.settings()
		m.mu.RLock()
		configsCopy := make(map[string]DeviceConfig, len(m.configs))
		for k, v := range m.configs {
			configsCopy[k] = v
		}
		discovered := m.discoveredListLocked()
		up := m.mqttUp
		broker := m.mqttBroker
		errMsg := m.mqttError
		m.mu.RUnlock()
		answer(w, http.StatusOK, map[string]any{
			"assignments":          configsCopy,
			"discovered_plugs":     discovered,
			"mqtt_configured":      strings.TrimSpace(s.MQTTHost) != "",
			"mqtt_connected":       up,
			"mqtt_broker":          broker,
			"mqtt_error":           errMsg,
			"default_cooldown_sec": s.AutoOffCooldownSec,
		})
	})

	mux.HandleFunc("GET /devices/{device_id}", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("device_id")
		principal := routesapi.PrincipalOf(r)
		resp, err := m.buildStatus(r.Context(), principal, deviceID)
		if err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		answer(w, http.StatusOK, resp)
	})

	mux.HandleFunc("PUT /devices/{device_id}/config", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("device_id")
		principal := routesapi.PrincipalOf(r)
		var body DeviceConfig
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			refuse(w, r, InvalidInput.New(err.Error()))
			return
		}
		normalizedTopic := normalizeTopic(body.TasmotaTopic)
		if normalizedTopic != "" {
			m.mu.RLock()
			var conflictID string
			for otherID, otherCfg := range m.configs {
				if otherID != deviceID && otherCfg.TasmotaTopic == normalizedTopic {
					conflictID = otherID
					break
				}
			}
			m.mu.RUnlock()
			if conflictID != "" {
				refuse(w, r, TopicInUse.New("plug topic is already assigned to another printer").
					With(fault.StringParam("topic", normalizedTopic), fault.StringParam("device", conflictID)))
				return
			}
		}
		if err := m.saveConfig(r.Context(), deviceID, body); err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		resp, err := m.buildStatus(r.Context(), principal, deviceID)
		if err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		answer(w, http.StatusOK, resp)
	})

	mux.HandleFunc("POST /devices/{device_id}/power", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("device_id")
		principal := routesapi.PrincipalOf(r)
		var body struct {
			Action string `json:"action"` // "on", "off", "toggle"
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			refuse(w, r, InvalidInput.New(err.Error()))
			return
		}
		action := strings.ToLower(strings.TrimSpace(body.Action))
		if action != "on" && action != "off" && action != "toggle" {
			refuse(w, r, InvalidInput.New("action must be on, off, or toggle"))
			return
		}

		cfg, err := m.getConfig(r.Context(), deviceID)
		if err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		if cfg.TasmotaTopic == "" {
			refuse(w, r, NotConfigured.New("no Tasmota topic configured for this printer"))
			return
		}

		if action == "toggle" {
			m.mu.RLock()
			p := m.plugs[cfg.TasmotaTopic]
			cur := ""
			if p != nil {
				cur = p.Power
			}
			m.mu.RUnlock()
			if cur == "ON" {
				action = "off"
			} else {
				action = "on"
			}
		}

		if action == "off" {
			safety, err := m.checkSafety(r.Context(), principal, deviceID)
			if err != nil {
				refuse(w, r, InternalError.New(err.Error()))
				return
			}
			if !safety.CanTurnOff {
				refuse(w, r, PrinterBusy.New(safety.LockReason).With(fault.StringParam("state", safety.PrinterState)))
				return
			}
		}

		if err := m.publishPower(cfg.TasmotaTopic, action); err != nil {
			refuse(w, r, BrokerOffline.New(err.Error()))
			return
		}

		m.mu.Lock()
		p, ok := m.plugs[cfg.TasmotaTopic]
		if !ok {
			p = &PlugTelemetry{Topic: cfg.TasmotaTopic}
			m.plugs[cfg.TasmotaTopic] = p
		}
		p.Power = strings.ToUpper(action)
		now := time.Now()
		p.UpdatedAt = &now
		m.mu.Unlock()

		go func(topic string) {
			time.Sleep(400 * time.Millisecond)
			m.queryPlugState(topic)
		}(cfg.TasmotaTopic)

		resp, err := m.buildStatus(r.Context(), principal, deviceID)
		if err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		answer(w, http.StatusOK, resp)
	})

	mux.HandleFunc("POST /devices/{device_id}/auto-off", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("device_id")
		principal := routesapi.PrincipalOf(r)
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			refuse(w, r, InvalidInput.New(err.Error()))
			return
		}

		cfg, err := m.getConfig(r.Context(), deviceID)
		if err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		if cfg.TasmotaTopic == "" {
			refuse(w, r, NotConfigured.New("no Tasmota topic configured for this printer"))
			return
		}
		cfg.AutoOffAfterPrint = body.Enabled
		if err := m.saveConfig(r.Context(), deviceID, cfg); err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}

		resp, err := m.buildStatus(r.Context(), principal, deviceID)
		if err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		answer(w, http.StatusOK, resp)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		refuse(w, r, RouteNotFound.New("unknown path").With(fault.StringParam("path", r.URL.Path)))
	})

	return mux
}

func answer(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error                { return os.Stdin.Close() }
