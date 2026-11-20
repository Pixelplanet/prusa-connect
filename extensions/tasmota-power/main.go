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
	"github.com/prusa3d/prusa-connect/sdk/accessapi"
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
	release = extension.Version("0.5.6")

	defaultMQTTPort     = 1883
	defaultMQTTTLSPort  = 8883
	defaultMQTTClientID = "prusa-connect-tasmota"
	defaultCooldownSec  = 120
	pollIntervalSec     = 5
	configsFileName     = "configs.json"
	settingsFileName    = "settings.json"
	usageFileName       = "usage.json"
	// maxPowerSamples caps the samples kept per printer: an hour at one
	// sample every two seconds, the shortest step between two samples.
	maxPowerSamples = 1800
	maxPrintHistory = 10
)

//go:embed openapi.json
var openAPI []byte

// Settings holds structured MQTT broker and default auto-off settings.
// Persisted in settings.json under the module's granted data directory.
type Settings struct {
	MQTTBuiltin        bool   `json:"mqtt_builtin"`
	MQTTHost           string `json:"mqtt_host"`
	MQTTPort           int    `json:"mqtt_port"`
	MQTTTLS            bool   `json:"mqtt_tls"`
	MQTTUsername       string `json:"mqtt_username"`
	MQTTPassword       string `json:"mqtt_password,omitempty"`
	HasPassword        bool   `json:"has_password"`
	KeepPassword       bool   `json:"keep_password,omitempty"`
	MQTTClientID       string `json:"mqtt_client_id"`
	HAURL              string `json:"ha_url,omitempty"`
	HAToken            string `json:"ha_token,omitempty"`
	HasHAToken         bool   `json:"has_ha_token"`
	KeepHAToken        bool   `json:"keep_ha_token,omitempty"`
	AutoOffCooldownSec int    `json:"auto_off_cooldown_sec"`
	ConfirmTurnOff     *bool  `json:"confirm_turn_off"`

	// Legacy option from 0.1.0; parsed into MQTTHost/MQTTPort/MQTTTLS when present.
	MQTTBroker string `json:"mqtt_broker,omitempty"`
}

// builtinListenName is the name module.yaml gives the built-in broker's port
// under needs.listen. The operator approves the port and may move it.
const builtinListenName = "mqtt"

// hostSettings is what the host sends on plugin.configure: the options
// module.yaml declares, as the operator wrote them on the extension's own
// Settings tab or in the server's configuration. The host decodes strictly, so
// every declared option needs a field here: a key with no field refuses the
// start.
type hostSettings struct {
	Settings

	// HTTPHost is the http_host option. A plug's address is kept per printer,
	// so this one only fills in the address field when a plug is set up.
	HTTPHost string `json:"http_host,omitempty"`
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
		if s.MQTTTLS && !s.MQTTBuiltin {
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
	s.HAURL = normalizeBaseURL(s.HAURL)
	s.HAToken = strings.TrimSpace(s.HAToken)
	s.HasPassword = s.MQTTPassword != ""
	s.HasHAToken = s.HAToken != ""
	return s
}

func parseBrokerURL(raw string) (host string, port int, useTLS bool) {
	b := strings.TrimSpace(raw)
	if b == "" {
		return "", 0, false
	}
	// The scheme, when one was written, says only whether TLS is wanted.
	if scheme, rest, found := strings.Cut(b, "://"); found {
		scheme = strings.ToLower(scheme)
		useTLS = scheme == "ssl" || scheme == "tls" || scheme == "mqtts"
		b = rest
	}
	b, _, _ = strings.Cut(b, "/")
	if at := strings.LastIndex(b, "@"); at >= 0 {
		b = b[at+1:]
	}
	if h, p, err := net.SplitHostPort(b); err == nil {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			port = n
		}
		return h, port, useTLS
	}
	return strings.Trim(b, "[]"), 0, useTLS
}

func (s Settings) mqttConfigured() bool {
	return s.MQTTBuiltin || strings.TrimSpace(s.MQTTHost) != ""
}

func (s Settings) haConfigured() bool {
	return normalizeBaseURL(s.HAURL) != "" && strings.TrimSpace(s.HAToken) != ""
}

// builtinCredentialsSet answers whether the built-in broker has the user name
// and password it needs to start.
func (s Settings) builtinCredentialsSet() bool {
	return strings.TrimSpace(s.MQTTUsername) != "" && s.MQTTPassword != ""
}

// errBuiltinNeedsCredentials is what the settings say while the built-in
// broker is switched on without a user name and a password.
const errBuiltinNeedsCredentials = "The built-in broker needs a user name and a password, so that only your plugs can sign in to it. Set both on the Connections tab, then set the same values in each plug."

// inProcessBroker names the built-in broker in this module's MQTT client. The
// client never dials it: its connection function opens a pipe into the broker
// in this process (EmbeddedBroker.Pipe), so no socket is opened.
const inProcessBroker = "built-in-broker"

// builtinLabel is how the settings name the built-in broker.
func builtinLabel(port int) string {
	return fmt.Sprintf("built-in broker on port %d", port)
}

// pipeInto makes an MQTT client reach brk in process.
func pipeInto(opts *mqtt.ClientOptions, brk *EmbeddedBroker) *mqtt.ClientOptions {
	return opts.SetCustomOpenConnectionFn(func(*url.URL, mqtt.ClientOptions) (net.Conn, error) {
		return brk.Pipe()
	})
}

// brokerURL is the address of the external broker the operator set, which
// this module's MQTT client dials: the mqtt_host option or the host on the
// Connections tab. It is empty for the built-in broker, which is reached in
// process.
func (s Settings) brokerURL() string {
	if s.MQTTBuiltin {
		return ""
	}
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
	out.HasHAToken = s.HAToken != ""
	out.HAToken = ""
	out.KeepHAToken = false
	out.MQTTBroker = ""
	return out
}

// DeviceConfig is persisted per printer in configs.json.
type DeviceConfig struct {
	Driver                string `json:"driver,omitempty"` // tasmota, tasmota_http, shelly_gen1, shelly_gen2, shelly_http, zigbee2mqtt, esphome, openbeken, generic_mqtt, homeassistant
	Mode                  string `json:"mode,omitempty"`   // legacy: "mqtt" or "http"
	TasmotaTopic          string `json:"tasmota_topic"`
	HTTPHost              string `json:"http_host,omitempty"`
	HTTPUser              string `json:"http_user,omitempty"`
	HTTPPassword          string `json:"http_password,omitempty"`
	HasHTTPPassword       bool   `json:"has_http_password,omitempty"`
	KeepHTTPPassword      bool   `json:"keep_http_password,omitempty"`
	MQTTCommandTopic      string `json:"mqtt_command_topic,omitempty"`
	MQTTStateTopic        string `json:"mqtt_state_topic,omitempty"`
	MQTTAvailabilityTopic string `json:"mqtt_availability_topic,omitempty"`
	MQTTPayloadOn         string `json:"mqtt_payload_on,omitempty"`
	MQTTPayloadOff        string `json:"mqtt_payload_off,omitempty"`
	MQTTStateOn           string `json:"mqtt_state_on,omitempty"`
	MQTTStateOff          string `json:"mqtt_state_off,omitempty"`
	MQTTStateJSONKey      string `json:"mqtt_state_json_key,omitempty"`
	MQTTPowerTopic        string `json:"mqtt_power_topic,omitempty"`
	MQTTPowerJSONKey      string `json:"mqtt_power_json_key,omitempty"`
	HAEntityID            string `json:"ha_entity_id,omitempty"`
	HAPowerEntityID       string `json:"ha_power_entity_id,omitempty"`
	RelayIndex            int    `json:"relay_index,omitempty"`
	// AutoOffAfterPrint says whether the plug turns off after the print that
	// runs now, or the next one. AutoOffDefault is what it goes back to once
	// that print is over: a printer that turns off after every print has both.
	AutoOffAfterPrint bool `json:"auto_off_after_print"`
	AutoOffDefault    bool `json:"auto_off_default,omitempty"`
	CooldownSec       *int `json:"cooldown_sec,omitempty"`
}

func (c DeviceConfig) relayIndex() int {
	if c.RelayIndex <= 0 {
		return 1
	}
	if c.RelayIndex > 8 {
		return 8
	}
	return c.RelayIndex
}

func (c DeviceConfig) isHTTP() bool {
	return c.usesHTTP()
}

func (c DeviceConfig) configured() bool {
	switch c.effectiveDriver() {
	case DriverTasmota:
		return normalizeTopic(c.TasmotaTopic) != ""
	case DriverTasmotaHTTP, DriverShellyHTTP:
		return normalizeHTTPHost(c.HTTPHost) != ""
	case DriverShellyGen1:
		return normalizeShellyGen1ID(c.TasmotaTopic) != ""
	case DriverShellyGen2, DriverESPHome, DriverOpenBeken:
		return normalizeMQTTPrefix(c.TasmotaTopic) != ""
	case DriverZigbee2MQTT:
		return normalizeZ2MTopic(c.TasmotaTopic) != ""
	case DriverGenericMQTT:
		return strings.TrimSpace(c.MQTTCommandTopic) != ""
	case DriverHomeAssistant:
		return strings.TrimSpace(c.HAEntityID) != ""
	default:
		return false
	}
}

func (c DeviceConfig) plugKey() string {
	switch c.effectiveDriver() {
	case DriverTasmota:
		return normalizeTopic(c.TasmotaTopic)
	case DriverTasmotaHTTP:
		if h := normalizeHTTPHost(c.HTTPHost); h != "" {
			return "http:" + strings.ToLower(h)
		}
	case DriverShellyHTTP:
		if h := normalizeHTTPHost(c.HTTPHost); h != "" {
			return "shellyhttp:" + strings.ToLower(h)
		}
	case DriverShellyGen1:
		if id := normalizeShellyGen1ID(c.TasmotaTopic); id != "" {
			return "shelly1:" + id
		}
	case DriverShellyGen2:
		if p := normalizeMQTTPrefix(c.TasmotaTopic); p != "" {
			return "shelly2:" + p
		}
	case DriverZigbee2MQTT:
		if b := normalizeZ2MTopic(c.TasmotaTopic); b != "" {
			return "z2m:" + b
		}
	case DriverESPHome:
		if p := normalizeMQTTPrefix(c.TasmotaTopic); p != "" {
			return "esphome:" + p
		}
	case DriverOpenBeken:
		if p := normalizeMQTTPrefix(c.TasmotaTopic); p != "" {
			return "openbeken:" + p
		}
	case DriverGenericMQTT:
		if cmd := strings.TrimSpace(c.MQTTCommandTopic); cmd != "" {
			return "generic:" + cmd
		}
	case DriverHomeAssistant:
		if ent := strings.TrimSpace(c.HAEntityID); ent != "" {
			return "ha:" + strings.ToLower(ent)
		}
	}
	return ""
}

func (c DeviceConfig) httpBaseURL() string {
	h := normalizeHTTPHost(c.HTTPHost)
	if h == "" {
		return ""
	}
	if strings.HasPrefix(h, "http://") || strings.HasPrefix(h, "https://") {
		return h
	}
	return "http://" + h
}

func (c DeviceConfig) redacted() DeviceConfig {
	out := c
	out.HasHTTPPassword = c.HTTPPassword != ""
	out.HTTPPassword = ""
	out.KeepHTTPPassword = false
	return out
}

func normalizeHTTPHost(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || strings.TrimSpace(u.Host) == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	host := strings.TrimSpace(u.Host)
	if scheme == "https" {
		return "https://" + host
	}
	return host
}

// PlugTelemetry holds live state for a smart plug.
type PlugTelemetry struct {
	Topic        string         `json:"topic,omitempty"`
	Driver       string         `json:"driver,omitempty"`
	FriendlyName string         `json:"friendly_name,omitempty"`
	Model        string         `json:"model,omitempty"`
	RelayCount   int            `json:"relay_count,omitempty"`
	RelayNames   []string       `json:"relay_names,omitempty"`
	Relays       map[int]string `json:"relays,omitempty"`
	Power        string         `json:"power"`        // "ON", "OFF", or "UNKNOWN"
	Online       *bool          `json:"online"`       // from tele/<topic>/LWT
	PowerWatts   *float64       `json:"power_watts"`  // from tele/<topic>/SENSOR ENERGY.Power
	Voltage      *float64       `json:"voltage"`      // from tele/<topic>/SENSOR ENERGY.Voltage
	CurrentAmps  *float64       `json:"current_amps"` // from tele/<topic>/SENSOR ENERGY.Current
	LastError    string         `json:"last_error,omitempty"`
	UpdatedAt    *time.Time     `json:"updated_at,omitempty"`

	MAC         string    `json:"-"`
	FullTopic   string    `json:"-"`
	Prefixes    [3]string `json:"-"`
	StateTexts  [4]string `json:"-"`
	OnlineText  string    `json:"-"`
	OfflineText string    `json:"-"`
}

func normalizeRelayIndex(idx int) int {
	if idx <= 0 {
		return 1
	}
	if idx > 8 {
		return 8
	}
	return idx
}

func (p *PlugTelemetry) clone() PlugTelemetry {
	cp := *p
	if len(p.RelayNames) > 0 {
		cp.RelayNames = append([]string(nil), p.RelayNames...)
	}
	if len(p.Relays) > 0 {
		cp.Relays = make(map[int]string, len(p.Relays))
		for k, v := range p.Relays {
			cp.Relays[k] = v
		}
	}
	if p.Online != nil {
		v := *p.Online
		cp.Online = &v
	}
	if p.PowerWatts != nil {
		v := *p.PowerWatts
		cp.PowerWatts = &v
	}
	if p.Voltage != nil {
		v := *p.Voltage
		cp.Voltage = &v
	}
	if p.CurrentAmps != nil {
		v := *p.CurrentAmps
		cp.CurrentAmps = &v
	}
	if p.UpdatedAt != nil {
		v := *p.UpdatedAt
		cp.UpdatedAt = &v
	}
	return cp
}

func (p *PlugTelemetry) relayState(idx int) string {
	idx = normalizeRelayIndex(idx)
	if p.Relays != nil {
		if st := p.Relays[idx]; st != "" {
			return st
		}
	}
	if idx == 1 && p.Power != "" {
		return p.Power
	}
	return "UNKNOWN"
}

func (p *PlugTelemetry) setRelayState(idx int, state string) {
	idx = normalizeRelayIndex(idx)
	if p.Relays == nil {
		p.Relays = make(map[int]string)
	}
	p.Relays[idx] = state
	if p.RelayCount < idx {
		p.RelayCount = idx
	}
	if idx == 1 {
		p.Power = state
	}
}

func (p *PlugTelemetry) formatTopic(prefixIdx int, leaf string) string {
	if prefixIdx < 0 || prefixIdx > 2 {
		prefixIdx = 0
	}
	defaults := [3]string{"cmnd", "stat", "tele"}
	prefix := p.Prefixes[prefixIdx]
	if prefix == "" {
		prefix = defaults[prefixIdx]
	}
	ft := p.FullTopic
	if ft == "" {
		ft = "%prefix%/%topic%/"
	}
	base := strings.ReplaceAll(ft, "%prefix%", prefix)
	base = strings.ReplaceAll(base, "%topic%", p.Topic)
	base = strings.Trim(base, "/")
	if leaf == "" {
		if base == "" {
			return ""
		}
		return base + "/"
	}
	if base == "" {
		return leaf
	}
	return base + "/" + leaf
}

func (p *PlugTelemetry) hasCustomFullTopic() bool {
	if p.FullTopic != "" && p.FullTopic != "%prefix%/%topic%/" {
		return true
	}
	defaults := [3]string{"cmnd", "stat", "tele"}
	for i, d := range defaults {
		if p.Prefixes[i] != "" && p.Prefixes[i] != d {
			return true
		}
	}
	return false
}

func (p *PlugTelemetry) normalizePowerValue(raw string) (string, bool) {
	v := strings.Trim(strings.TrimSpace(raw), "\"")
	if v == "" {
		return "", false
	}
	if p.StateTexts[1] != "" && strings.EqualFold(v, p.StateTexts[1]) {
		return "ON", true
	}
	if p.StateTexts[0] != "" && strings.EqualFold(v, p.StateTexts[0]) {
		return "OFF", true
	}
	switch strings.ToUpper(v) {
	case "ON", "1", "TRUE":
		return "ON", true
	case "OFF", "0", "FALSE":
		return "OFF", true
	default:
		return "", false
	}
}

func (p *PlugTelemetry) onlineWord() string {
	if p.OnlineText != "" {
		return p.OnlineText
	}
	return "Online"
}

func (p *PlugTelemetry) offlineWord() string {
	if p.OfflineText != "" {
		return p.OfflineText
	}
	return "Offline"
}

type stateEvent struct {
	State string
	Err   string
}

type stateWaiter struct {
	topic string
	relay int
	ch    chan stateEvent
}

var errBrokerOffline = errors.New("mqtt broker not connected")

type autoOffTracker struct {
	sawActive  bool
	finishedAt *time.Time
	// fired says the plug was turned off for the print before. Nothing more
	// happens until the printer is seen printing again.
	fired bool
	// jobID is the print that was seen running, when the printer named it.
	jobID string
	// kept says why the printer stayed on after its last print: cancelled or
	// failed. It is empty once a new print is seen.
	kept string
	// offFault is why the plug could not be turned off when the wait was
	// over. The turn-off is tried again until it works.
	offFault string
}

// skippedPrint follows the one print a person switched auto-off away for, on
// a printer that turns off by default.
type skippedPrint struct {
	sawPrint bool
	ended    bool
	jobID    string
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
	// access is the host's access.check, asked before a route changes
	// anything (access.go).
	access accessChecker
	// hints keeps the answers that only decide what a page offers.
	hints hintCache
	// principal reads the person a request is for. It is nil outside tests,
	// which read it with routesapi.PrincipalOf.
	principal func(*http.Request) string

	held atomic.Pointer[Settings]

	mu      sync.RWMutex
	dataDir string
	// What the host sent on plugin.configure. A pinned value is one the
	// operator wrote in the host's own settings; it wins over settings.json.
	pinnedMQTTHost    string
	pinnedHAURL       string
	suggestedHTTPHost string
	// builtinPort is the port granted for the built-in broker, 0 when none is.
	builtinPort    int
	mqttClient     mqtt.Client
	mqttUp         bool
	mqttBroker     string
	mqttError      string
	mqttWarning    string
	embeddedBroker *EmbeddedBroker
	httpClient     *http.Client
	plugs          map[string]*PlugTelemetry
	configs        map[string]DeviceConfig
	trackers       map[string]*autoOffTracker
	skips          map[string]*skippedPrint
	axes           map[string]*axisSample
	usage          map[string]*DeviceUsage
	lastUsageSaved time.Time
	waiters        []*stateWaiter
}

func newModule(conn *wirerpc.Conn) *module {
	m := &module{
		devices:    deviceapi.NewClient(conn),
		access:     accessapi.NewClient(conn),
		httpClient: &http.Client{Timeout: 5 * time.Second},
		plugs:      make(map[string]*PlugTelemetry),
		configs:    make(map[string]DeviceConfig),
		trackers:   make(map[string]*autoOffTracker),
		skips:      make(map[string]*skippedPrint),
		axes:       make(map[string]*axisSample),
		usage:      make(map[string]*DeviceUsage),
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

// setHostConfig keeps what plugin.configure carried beside the settings: the
// values the operator pinned in the host's own settings, and the port granted
// for the built-in broker.
func (m *module) setHostConfig(sent hostSettings, grants wirerpc.Grants) {
	port := 0
	if grant, ok := grants.Listen[builtinListenName]; ok && grant.Port > 0 {
		port = grant.Port
	}
	m.mu.Lock()
	m.pinnedMQTTHost = strings.TrimSpace(sent.MQTTHost)
	m.pinnedHAURL = normalizeBaseURL(sent.HAURL)
	m.suggestedHTTPHost = normalizeHTTPHost(sent.HTTPHost)
	m.builtinPort = port
	m.mu.Unlock()
}

// startSettings is what the module runs on after plugin.configure: what
// settings.json holds, or the defaults on an install that has saved nothing
// yet, with the values the operator pinned written over either.
func (m *module) startSettings(sent hostSettings) Settings {
	effective := defaultSettings()
	if stored, ok := m.loadSettingsFromDisk(); ok {
		effective = stored
	} else if legacy := normalizeSettings(sent.Settings); legacy.MQTTHost != "" || legacy.MQTTBuiltin || legacy.HAURL != "" {
		// Migrate legacy host-passed settings into settings.json once.
		effective = legacy
		_ = m.writeSettingsToDisk(effective)
	}

	return m.applyHostPins(effective)
}

// applyHostPins writes the values the operator pinned in the host's settings
// over s, so the two places a host can be written never disagree.
func (m *module) applyHostPins(s Settings) Settings {
	m.mu.RLock()
	host, haURL := m.pinnedMQTTHost, m.pinnedHAURL
	m.mu.RUnlock()
	if host != "" {
		s.MQTTHost = host
	}
	if haURL != "" {
		s.HAURL = haURL
	}
	return normalizeSettings(s)
}

func (m *module) grantedBuiltinPort() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.builtinPort
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
			wirerpc.MethodConfigure: wirerpc.ConfigureGranted(func(_ context.Context, sent hostSettings, grants wirerpc.Grants) error {
				if grants.Data != "" {
					mod.setDataDir(grants.Data)
					mod.loadAllConfigs()
					mod.loadAllUsage()
				}
				mod.setHostConfig(sent, grants)

				effective := mod.startSettings(sent)

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
		mod.disconnectMQTT()
		return fmt.Errorf("serve: %w", err)
	}
	mod.disconnectMQTT()
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
	toWrite.HasHAToken = false
	toWrite.KeepHAToken = false
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
		m.configs[id] = normalizeDeviceConfig(cfg)
	}
	m.mu.Unlock()

	m.syncDriverSubscriptions()
	for _, cfg := range loaded {
		ncfg := normalizeDeviceConfig(cfg)
		if ncfg.configured() {
			go m.refreshConfigState(context.Background(), ncfg)
		}
	}
}

func (m *module) refreshConfigState(ctx context.Context, cfg DeviceConfig) {
	switch cfg.effectiveDriver() {
	case DriverTasmota:
		if cfg.TasmotaTopic != "" {
			m.queryPlugState(cfg.TasmotaTopic)
		}
	case DriverTasmotaHTTP:
		if cfg.HTTPHost != "" {
			_ = m.pollHTTPPlug(ctx, cfg)
		}
	case DriverShellyHTTP:
		if cfg.HTTPHost != "" {
			_ = m.pollShellyHTTPPlug(ctx, cfg)
		}
	case DriverHomeAssistant:
		if cfg.HAEntityID != "" {
			_ = m.pollHomeAssistantPlug(ctx, cfg)
		}
	case DriverShellyGen1, DriverShellyGen2, DriverZigbee2MQTT, DriverESPHome, DriverOpenBeken, DriverGenericMQTT:
		m.syncDriverSubscriptions()
		m.queryDriverState(cfg)
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
		u.History = trimHistory(u.History, time.Now(), powerHistoryKeep)
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

func normalizeDeviceConfig(cfg DeviceConfig) DeviceConfig {
	d := cfg.effectiveDriver()

	clearHTTP := func() {
		cfg.HTTPHost = ""
		cfg.HTTPUser = ""
		cfg.HTTPPassword = ""
	}
	clearGeneric := func() {
		cfg.MQTTCommandTopic = ""
		cfg.MQTTStateTopic = ""
		cfg.MQTTAvailabilityTopic = ""
		cfg.MQTTPayloadOn = ""
		cfg.MQTTPayloadOff = ""
		cfg.MQTTStateOn = ""
		cfg.MQTTStateOff = ""
		cfg.MQTTStateJSONKey = ""
		cfg.MQTTPowerTopic = ""
		cfg.MQTTPowerJSONKey = ""
	}
	clearHA := func() {
		cfg.HAEntityID = ""
		cfg.HAPowerEntityID = ""
	}

	switch d {
	case DriverTasmotaHTTP, DriverShellyHTTP:
		cfg.HTTPHost = normalizeHTTPHost(cfg.HTTPHost)
		cfg.HTTPUser = strings.TrimSpace(cfg.HTTPUser)
		cfg.TasmotaTopic = ""
		clearGeneric()
		clearHA()
		if cfg.HTTPHost == "" {
			cfg.Driver = ""
			cfg.Mode = ""
			clearHTTP()
		} else {
			cfg.Driver = d
			cfg.Mode = "http"
		}
	case DriverHomeAssistant:
		cfg.HAEntityID = strings.TrimSpace(cfg.HAEntityID)
		cfg.HAPowerEntityID = strings.TrimSpace(cfg.HAPowerEntityID)
		cfg.TasmotaTopic = ""
		clearHTTP()
		clearGeneric()
		if cfg.HAEntityID == "" {
			cfg.Driver = ""
			cfg.Mode = ""
			clearHA()
		} else {
			cfg.Driver = d
			cfg.Mode = "ha"
		}
	case DriverGenericMQTT:
		cfg.MQTTCommandTopic = strings.TrimSpace(cfg.MQTTCommandTopic)
		cfg.MQTTStateTopic = strings.TrimSpace(cfg.MQTTStateTopic)
		cfg.MQTTAvailabilityTopic = strings.TrimSpace(cfg.MQTTAvailabilityTopic)
		cfg.MQTTPayloadOn = strings.TrimSpace(cfg.MQTTPayloadOn)
		cfg.MQTTPayloadOff = strings.TrimSpace(cfg.MQTTPayloadOff)
		cfg.MQTTStateOn = strings.TrimSpace(cfg.MQTTStateOn)
		cfg.MQTTStateOff = strings.TrimSpace(cfg.MQTTStateOff)
		cfg.MQTTStateJSONKey = strings.TrimSpace(cfg.MQTTStateJSONKey)
		cfg.MQTTPowerTopic = strings.TrimSpace(cfg.MQTTPowerTopic)
		cfg.MQTTPowerJSONKey = strings.TrimSpace(cfg.MQTTPowerJSONKey)
		cfg.TasmotaTopic = ""
		clearHTTP()
		clearHA()
		if cfg.MQTTCommandTopic == "" {
			cfg.Driver = ""
			cfg.Mode = ""
			clearGeneric()
		} else {
			cfg.Driver = d
			cfg.Mode = "mqtt"
		}
	case DriverShellyGen1:
		cfg.TasmotaTopic = normalizeShellyGen1ID(cfg.TasmotaTopic)
		clearHTTP()
		clearGeneric()
		clearHA()
		if cfg.TasmotaTopic == "" {
			cfg.Driver = ""
			cfg.Mode = ""
		} else {
			cfg.Driver = d
			cfg.Mode = "mqtt"
		}
	case DriverZigbee2MQTT:
		cfg.TasmotaTopic = normalizeZ2MTopic(cfg.TasmotaTopic)
		clearHTTP()
		clearGeneric()
		clearHA()
		if cfg.TasmotaTopic == "" {
			cfg.Driver = ""
			cfg.Mode = ""
		} else {
			cfg.Driver = d
			cfg.Mode = "mqtt"
		}
	case DriverShellyGen2, DriverESPHome, DriverOpenBeken:
		cfg.TasmotaTopic = normalizeMQTTPrefix(cfg.TasmotaTopic)
		clearHTTP()
		clearGeneric()
		clearHA()
		if cfg.TasmotaTopic == "" {
			cfg.Driver = ""
			cfg.Mode = ""
		} else {
			cfg.Driver = d
			cfg.Mode = "mqtt"
		}
	default:
		cfg.TasmotaTopic = normalizeTopic(cfg.TasmotaTopic)
		clearHTTP()
		clearGeneric()
		clearHA()
		if cfg.TasmotaTopic != "" {
			cfg.Driver = DriverTasmota
			cfg.Mode = "mqtt"
		} else {
			cfg.Driver = ""
			cfg.Mode = ""
		}
	}

	cfg.HasHTTPPassword = cfg.HTTPPassword != ""
	cfg.KeepHTTPPassword = false
	if !cfg.configured() {
		cfg.RelayIndex = 0
	} else if cfg.Driver == DriverHomeAssistant || cfg.Driver == DriverGenericMQTT {
		cfg.RelayIndex = 1
	} else {
		cfg.RelayIndex = cfg.relayIndex()
	}
	return cfg
}

func (m *module) saveConfig(_ context.Context, deviceID string, cfg DeviceConfig) error {
	keepHTTPPw := cfg.KeepHTTPPassword
	cfg = normalizeDeviceConfig(cfg)

	m.mu.Lock()
	if existing, ok := m.configs[deviceID]; ok && cfg.usesHTTP() && cfg.HTTPHost != "" && cfg.HTTPPassword == "" && keepHTTPPw {
		cfg.HTTPPassword = existing.HTTPPassword
		cfg.HasHTTPPassword = cfg.HTTPPassword != ""
	}
	m.configs[deviceID] = cfg
	if !cfg.AutoOffAfterPrint {
		delete(m.trackers, deviceID)
	}
	if cfg.AutoOffAfterPrint || !cfg.AutoOffDefault {
		delete(m.skips, deviceID)
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

	if cfg.configured() {
		go m.refreshConfigState(context.Background(), cfg)
	}
	return nil
}

func normalizeTopic(t string) string {
	t = strings.TrimSpace(t)
	t = strings.Trim(t, "/")
	t = strings.TrimPrefix(t, "cmnd/")
	t = strings.TrimPrefix(t, "stat/")
	t = strings.TrimPrefix(t, "tele/")
	if idx := strings.LastIndex(t, "/"); idx >= 0 {
		if parsePowerLeafIndex(t[idx+1:]) >= 0 {
			t = t[:idx]
		}
	}
	return strings.Trim(t, "/")
}

// --- MQTT ---

func (m *module) disconnectMQTT() {
	m.mu.Lock()
	c := m.mqttClient
	m.mqttClient = nil
	m.mqttUp = false
	brk := m.embeddedBroker
	m.embeddedBroker = nil
	m.mu.Unlock()
	if c != nil && c.IsConnected() {
		c.Disconnect(250)
	}
	if brk != nil {
		_ = brk.Close()
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

// probeBuiltinBrokerReachability checks, while the built-in broker runs, that
// the address the operator gave plugs for this server reaches it. That address
// is the broker host on the Connections tab, or the mqtt_host option, which
// module.yaml declares under egress. It opens one TCP connection to that host
// on the granted broker port and closes it again, sending nothing.
func (m *module) probeBuiltinBrokerReachability(brk *EmbeddedBroker, rawHost string) string {
	if brk == nil {
		return ""
	}
	host, _, _ := parseBrokerURL(strings.TrimSpace(rawHost))
	if host == "" || host == "127.0.0.1" || host == "::1" || strings.EqualFold(host, "localhost") {
		return ""
	}
	before := brk.AcceptedCount()
	target := net.JoinHostPort(host, strconv.Itoa(brk.Port()))
	conn, err := net.DialTimeout("tcp", target, 1500*time.Millisecond)
	if err != nil {
		return fmt.Sprintf("Built-in broker is listening internally on :%d, but %s is not reachable (%v). If Prusa Connect runs in a Docker container (bridge network), publish TCP port %d (-p %d:%d) on the container so smart plugs on your LAN can reach it — or connect the plug directly by its IP address.", brk.Port(), target, err, brk.Port(), brk.Port(), brk.Port())
	}
	time.Sleep(60 * time.Millisecond)
	_ = conn.Close()
	if brk.AcceptedCount() == before {
		return fmt.Sprintf("Another MQTT broker is already running on your network at %s! Smart plugs connecting to %s will reach that existing broker instead of this built-in broker. Disable the built-in broker and use your existing broker instead.", target, target)
	}
	return ""
}

func (m *module) reconnectMQTT(s Settings) {
	m.disconnectMQTT()
	port := m.grantedBuiltinPort()
	brokerURL := s.brokerURL()
	label := brokerURL
	if s.MQTTBuiltin && port > 0 {
		label = builtinLabel(port)
	}
	m.mu.Lock()
	m.mqttBroker = label
	m.mqttError = ""
	m.mqttWarning = ""
	m.mu.Unlock()

	if s.MQTTBuiltin {
		if port <= 0 {
			// The module listens only on a port the operator granted.
			errMsg := "The built-in broker is switched on, but its port is not approved. Approve it on the Ports tab of this extension, then save the connection again."
			m.mu.Lock()
			m.mqttError = errMsg
			m.mu.Unlock()
			fmt.Fprintf(os.Stderr, "%s: %s\n", logName, errMsg)
			return
		}
		if !s.builtinCredentialsSet() {
			m.mu.Lock()
			m.mqttError = errBuiltinNeedsCredentials
			m.mu.Unlock()
			fmt.Fprintf(os.Stderr, "%s: %s\n", logName, errBuiltinNeedsCredentials)
			return
		}
		listenAddr := fmt.Sprintf(":%d", port)
		brk, err := StartEmbeddedBroker(listenAddr, s.MQTTUsername, s.MQTTPassword)
		if err != nil {
			errMsg := fmt.Sprintf("Port %d is already in use on this host (%v). Another MQTT broker appears to be running — disable the built-in broker and connect to your existing broker instead.", port, err)
			m.mu.Lock()
			m.mqttError = errMsg
			m.mu.Unlock()
			fmt.Fprintf(os.Stderr, "%s: %s\n", logName, errMsg)
			return
		}
		m.mu.Lock()
		m.embeddedBroker = brk
		m.mu.Unlock()
		fmt.Fprintf(os.Stderr, "%s: started built-in MQTT broker on %s\n", logName, listenAddr)
		if warn := m.probeBuiltinBrokerReachability(brk, s.MQTTHost); warn != "" {
			m.mu.Lock()
			m.mqttWarning = warn
			m.mu.Unlock()
			fmt.Fprintf(os.Stderr, "%s: warning: %s\n", logName, warn)
		}
	}

	var opts *mqtt.ClientOptions
	if s.MQTTBuiltin {
		m.mu.RLock()
		brk := m.embeddedBroker
		m.mu.RUnlock()
		opts = pipeInto(m.buildMQTTOptions(s, inProcessBroker, s.MQTTClientID), brk)
	} else {
		if brokerURL == "" {
			return
		}
		opts = m.buildMQTTOptions(s, brokerURL, s.MQTTClientID)
	}

	fmt.Fprintf(os.Stderr, "%s: connecting to MQTT broker %s (client_id=%s)\n", logName, label, s.MQTTClientID)

	opts.SetOnConnectHandler(func(c mqtt.Client) {
		m.mu.Lock()
		m.mqttUp = true
		m.mqttError = ""
		var customSubs []string
		for _, p := range m.plugs {
			if p.hasCustomFullTopic() {
				if subStat := p.formatTopic(1, "+"); subStat != "" {
					customSubs = append(customSubs, subStat)
				}
				if subTele := p.formatTopic(2, "+"); subTele != "" {
					customSubs = append(customSubs, subTele)
				}
			}
		}
		m.mu.Unlock()

		c.Subscribe("tasmota/discovery/+/config", 0, m.onMQTTMessage)
		c.Subscribe("stat/+/+", 0, m.onMQTTMessage)
		c.Subscribe("tele/+/LWT", 0, m.onMQTTMessage)
		c.Subscribe("tele/+/STATE", 0, m.onMQTTMessage)
		c.Subscribe("tele/+/SENSOR", 0, m.onMQTTMessage)
		for _, sub := range customSubs {
			c.Subscribe(sub, 0, m.onMQTTMessage)
		}

		go func() {
			m.syncDriverSubscriptions()
			m.queryAllKnownPlugs()
		}()
		fmt.Fprintf(os.Stderr, "%s: connected to MQTT broker %s\n", logName, label)
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
		fmt.Fprintf(os.Stderr, "%s: retrying MQTT connection to %s\n", logName, label)
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

func (m *module) testBroker(override *Settings) (bool, string, string, string) {
	saved := m.settings()
	candidate := saved
	if override != nil {
		candidate = *override
		if candidate.MQTTPassword == "" && candidate.KeepPassword {
			candidate.MQTTPassword = saved.MQTTPassword
		}
		candidate = m.applyHostPins(candidate)
	}

	if candidate.MQTTBuiltin {
		return m.testBuiltinBroker(candidate)
	}

	brokerURL := candidate.brokerURL()
	if brokerURL == "" {
		return false, "", "Broker host is required", ""
	}
	if err := tryConnect(m.buildMQTTOptions(candidate, brokerURL, testClientID(candidate))); err != nil {
		return false, brokerURL, err.Error(), ""
	}
	return true, brokerURL, "", ""
}

// testBuiltinBroker tests the built-in broker without listening anywhere but
// on the port the operator granted. While the broker runs it is asked in
// process; while it does not, it is started on that port for the test.
func (m *module) testBuiltinBroker(candidate Settings) (bool, string, string, string) {
	port := m.grantedBuiltinPort()
	if port <= 0 {
		return false, "", "The built-in broker's port is not approved. Approve it on the Ports tab of this extension.", ""
	}
	label := builtinLabel(port)
	if !candidate.builtinCredentialsSet() {
		return false, label, errBuiltinNeedsCredentials, ""
	}

	m.mu.RLock()
	brk := m.embeddedBroker
	m.mu.RUnlock()

	warnMsg := ""
	if brk == nil || brk.Port() != port {
		tmp, err := StartEmbeddedBroker(fmt.Sprintf(":%d", port), candidate.MQTTUsername, candidate.MQTTPassword)
		if err != nil {
			return false, label, fmt.Sprintf("Port %d is already in use on this host (%v). Another MQTT broker appears to be running.", port, err), ""
		}
		defer func() { _ = tmp.Close() }()
		brk = tmp
	} else if !brk.signsIn(candidate.MQTTUsername, candidate.MQTTPassword) {
		// The broker that runs keeps the saved user name and password until
		// the new ones are saved, so the test signs in with those.
		warnMsg = "The built-in broker runs with the saved user name and password. Save to use the new ones, and set them in each plug too."
		candidate.MQTTUsername, candidate.MQTTPassword = brk.username, brk.password
	}

	opts := pipeInto(m.buildMQTTOptions(candidate, inProcessBroker, testClientID(candidate)), brk)
	if err := tryConnect(opts); err != nil {
		return false, label, err.Error(), ""
	}
	if reach := m.probeBuiltinBrokerReachability(brk, candidate.MQTTHost); reach != "" {
		warnMsg = strings.TrimSpace(warnMsg + " " + reach)
	}
	return true, label, "", warnMsg
}

// testClientID is the client id of a test connection, apart from the module's
// own so the test does not push that client off the broker.
func testClientID(s Settings) string {
	return fmt.Sprintf("%s-test-%d", s.MQTTClientID, time.Now().UnixNano()%10000)
}

// tryConnect connects once with opts and disconnects again.
func tryConnect(opts *mqtt.ClientOptions) error {
	opts.SetConnectTimeout(5 * time.Second).SetAutoReconnect(false).SetConnectRetry(false)
	tc := mqtt.NewClient(opts)
	tok := tc.Connect()
	if !tok.WaitTimeout(6 * time.Second) {
		return errors.New("connection timed out after 6s")
	}
	if err := tok.Error(); err != nil {
		return err
	}
	tc.Disconnect(100)
	return nil
}

func (m *module) discoverPlugs() {
	m.mu.RLock()
	c := m.mqttClient
	up := m.mqttUp
	m.mu.RUnlock()
	if c == nil || !up {
		return
	}
	c.Subscribe("tasmota/discovery/+/config", 0, m.onMQTTMessage)
	m.queryAllKnownPlugs()
}

func isDriverPrefixedKey(topic string) bool {
	for _, p := range []string{"http:", "shellyhttp:", "shelly1:", "shelly2:", "z2m:", "esphome:", "openbeken:", "generic:", "ha:"} {
		if strings.HasPrefix(topic, p) {
			return true
		}
	}
	return false
}

func (m *module) discoveredListLocked() []PlugTelemetry {
	out := make([]PlugTelemetry, 0, len(m.plugs))
	for topic, p := range m.plugs {
		// Show auto-discovered Tasmota plugs and Shelly Gen1 plugs in the discovery list.
		if isDriverPrefixedKey(topic) && !strings.HasPrefix(topic, "shelly1:") {
			continue
		}
		cp := p.clone()
		if strings.HasPrefix(topic, "shelly1:") {
			cp.Topic = strings.TrimPrefix(topic, "shelly1:")
			if cp.Driver == "" {
				cp.Driver = DriverShellyGen1
			}
		} else {
			cp.Topic = topic
			if cp.Driver == "" {
				cp.Driver = DriverTasmota
			}
		}
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Driver != out[j].Driver {
			return out[i].Driver < out[j].Driver
		}
		return out[i].Topic < out[j].Topic
	})
	return out
}

// parsePowerLeafIndex returns 1 for "POWER" or "POWER1", 2..8 for "POWER2".."POWER8",
// 0 for "POWER0", or -1 if leaf is not a POWER leaf.
func parsePowerLeafIndex(leaf string) int {
	upper := strings.ToUpper(strings.TrimSpace(leaf))
	if upper == "POWER" {
		return 1
	}
	if !strings.HasPrefix(upper, "POWER") || len(upper) != 6 {
		return -1
	}
	ch := upper[5]
	if ch < '0' || ch > '8' {
		return -1
	}
	return int(ch - '0')
}

func (m *module) notifyWaitersLocked(topic string, relay int, evt stateEvent) {
	for _, w := range m.waiters {
		if w.topic == topic && (relay == 0 || w.relay == relay) {
			select {
			case w.ch <- evt:
			default:
			}
		}
	}
}

func (m *module) removeWaiter(target *stateWaiter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, w := range m.waiters {
		if w == target {
			m.waiters = append(m.waiters[:i], m.waiters[i+1:]...)
			break
		}
	}
}

func (m *module) resolveIncomingTopicLocked(msgTopic string) (plug *PlugTelemetry, kind string, leaf string, ok bool) {
	for _, p := range m.plugs {
		if !p.hasCustomFullTopic() {
			continue
		}
		if statPrefix := p.formatTopic(1, ""); statPrefix != "" && strings.HasPrefix(msgTopic, statPrefix) {
			rem := strings.TrimPrefix(msgTopic, statPrefix)
			if rem != "" && !strings.Contains(rem, "/") {
				return p, "stat", rem, true
			}
		}
		if telePrefix := p.formatTopic(2, ""); telePrefix != "" && strings.HasPrefix(msgTopic, telePrefix) {
			rem := strings.TrimPrefix(msgTopic, telePrefix)
			if rem != "" && !strings.Contains(rem, "/") {
				return p, "tele", rem, true
			}
		}
	}

	parts := strings.Split(msgTopic, "/")
	if len(parts) < 3 {
		return nil, "", "", false
	}

	var topic string
	switch {
	case parts[0] == "stat" || parts[0] == "tele":
		kind = parts[0]
		topic = strings.Join(parts[1:len(parts)-1], "/")
		leaf = parts[len(parts)-1]
	case parts[len(parts)-2] == "stat" || parts[len(parts)-2] == "tele":
		kind = parts[len(parts)-2]
		topic = strings.Join(parts[:len(parts)-2], "/")
		leaf = parts[len(parts)-1]
	default:
		return nil, "", "", false
	}

	p, exists := m.plugs[topic]
	if !exists {
		p = &PlugTelemetry{Topic: topic, Power: "UNKNOWN"}
		m.plugs[topic] = p
	}
	return p, kind, leaf, true
}

func (m *module) onMQTTMessage(c mqtt.Client, msg mqtt.Message) {
	now := time.Now()
	if m.handleDriverMQTTPacket(msg.Topic(), msg.Payload(), now) {
		return
	}
	customSubs, queryTopic := m.handleMQTTPacket(msg.Topic(), msg.Payload(), now)
	if c != nil && len(customSubs) > 0 {
		for _, sub := range customSubs {
			c.Subscribe(sub, 0, m.onMQTTMessage)
		}
	}
	if queryTopic != "" {
		go m.queryPlugState(queryTopic)
	}
}

func (m *module) handleMQTTPacket(msgTopic string, payloadBytes []byte, now time.Time) (customSubs []string, queryTopic string) {
	parts := strings.Split(msgTopic, "/")
	if len(parts) == 4 && parts[0] == "tasmota" && parts[1] == "discovery" && parts[3] == "config" {
		var disc struct {
			DN    string    `json:"dn"`
			FN    []*string `json:"fn"`
			HN    string    `json:"hn"`
			MD    string    `json:"md"`
			MAC   string    `json:"mac"`
			T     string    `json:"t"`
			FT    string    `json:"ft"`
			TP    []string  `json:"tp"`
			State []string  `json:"state"`
			Onln  string    `json:"onln"`
			Ofln  string    `json:"ofln"`
			RL    []int     `json:"rl"`
		}
		if json.Unmarshal(payloadBytes, &disc) != nil || strings.TrimSpace(disc.T) == "" {
			return nil, ""
		}

		m.mu.Lock()
		plug, ok := m.plugs[disc.T]
		if !ok {
			plug = &PlugTelemetry{Topic: disc.T, Power: "UNKNOWN"}
			m.plugs[disc.T] = plug
		}

		if len(disc.FN) > 0 && disc.FN[0] != nil && strings.TrimSpace(*disc.FN[0]) != "" {
			plug.FriendlyName = strings.TrimSpace(*disc.FN[0])
		} else if strings.TrimSpace(disc.DN) != "" {
			plug.FriendlyName = strings.TrimSpace(disc.DN)
		} else if strings.TrimSpace(disc.HN) != "" && plug.FriendlyName == "" {
			plug.FriendlyName = strings.TrimSpace(disc.HN)
		}

		if disc.MD != "" {
			plug.Model = disc.MD
		}
		if disc.MAC != "" {
			plug.MAC = disc.MAC
		}
		prevLWTTopic := plug.formatTopic(2, "LWT")
		if disc.FT != "" {
			plug.FullTopic = disc.FT
		}
		if len(disc.TP) == 3 {
			for i := 0; i < 3; i++ {
				if disc.TP[i] != "" {
					plug.Prefixes[i] = disc.TP[i]
				}
			}
		}
		if plug.formatTopic(2, "LWT") != prevLWTTopic {
			plug.Online = nil
		}
		for i := 0; i < len(disc.State) && i < 4; i++ {
			if disc.State[i] != "" {
				plug.StateTexts[i] = disc.State[i]
			}
		}
		if disc.Onln != "" {
			plug.OnlineText = disc.Onln
		}
		if disc.Ofln != "" {
			plug.OfflineText = disc.Ofln
		}

		relayCount := 0
		for i, kind := range disc.RL {
			if kind > 0 && i < 8 {
				relayCount = i + 1
			}
		}
		if relayCount < 1 && plug.RelayCount < 1 {
			relayCount = 1
		}
		if relayCount > 0 {
			plug.RelayCount = relayCount
			names := make([]string, relayCount)
			for i := 0; i < relayCount && i < len(disc.FN); i++ {
				if disc.FN[i] != nil {
					names[i] = strings.TrimSpace(*disc.FN[i])
				}
			}
			plug.RelayNames = names
		}

		plug.UpdatedAt = &now
		if plug.hasCustomFullTopic() {
			if s1 := plug.formatTopic(1, "+"); s1 != "" {
				customSubs = append(customSubs, s1)
			}
			if s2 := plug.formatTopic(2, "+"); s2 != "" {
				customSubs = append(customSubs, s2)
			}
		}
		queryTopic = plug.Topic
		m.mu.Unlock()
		return customSubs, queryTopic
	}

	payload := strings.TrimSpace(string(payloadBytes))
	if payload == "" {
		// Ignore empty payloads (e.g. PowerRetain 0 clearing retained state).
		return nil, ""
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	plug, kind, leaf, ok := m.resolveIncomingTopicLocked(msgTopic)
	if !ok {
		return nil, ""
	}

	switch {
	case kind == "tele" && leaf == "LWT":
		// Ignore stale LWT messages arriving on an old topic layout.
		if plug.FullTopic != "" && msgTopic != plug.formatTopic(2, "LWT") {
			return nil, ""
		}
		if strings.EqualFold(payload, plug.onlineWord()) {
			wasOnline := plug.Online != nil && *plug.Online
			online := true
			plug.Online = &online
			plug.UpdatedAt = &now
			if !wasOnline {
				queryTopic = plug.Topic
			}
		} else if strings.EqualFold(payload, plug.offlineWord()) {
			online := false
			plug.Online = &online
			plug.UpdatedAt = &now
		}

	case leaf == "SENSOR" || strings.EqualFold(leaf, "STATUS8") || strings.EqualFold(leaf, "STATUS10"):
		if !strings.HasPrefix(payload, "{") {
			return nil, ""
		}
		var sns struct {
			ENERGY    *energyPayload `json:"ENERGY"`
			StatusSNS *struct {
				ENERGY *energyPayload `json:"ENERGY"`
			} `json:"StatusSNS"`
		}
		if json.Unmarshal(payloadBytes, &sns) == nil {
			if sns.ENERGY != nil {
				applyEnergyReading(plug, sns.ENERGY, now)
			} else if sns.StatusSNS != nil && sns.StatusSNS.ENERGY != nil {
				applyEnergyReading(plug, sns.StatusSNS.ENERGY, now)
			}
		}

	case strings.EqualFold(leaf, "RESULT") || leaf == "STATE" || strings.EqualFold(leaf, "STATUS11") || parsePowerLeafIndex(leaf) >= 0:
		if strings.HasPrefix(payload, "{") {
			var rawMap map[string]json.RawMessage
			if json.Unmarshal(payloadBytes, &rawMap) != nil {
				return nil, ""
			}
			if stsRaw, hasSTS := rawMap["StatusSTS"]; hasSTS {
				var stsMap map[string]json.RawMessage
				if json.Unmarshal(stsRaw, &stsMap) == nil {
					rawMap = stsMap
				}
			}
			if cmdRaw, hasCmd := rawMap["Command"]; hasCmd {
				var cmdStr string
				if json.Unmarshal(cmdRaw, &cmdStr) == nil && strings.EqualFold(cmdStr, "Error") {
					plug.LastError = "Tasmota returned Command: Error"
					errRelay := 0
					if inRaw, hasIn := rawMap["Input"]; hasIn {
						var inStr string
						if json.Unmarshal(inRaw, &inStr) == nil {
							fields := strings.Fields(inStr)
							if len(fields) > 0 {
								if idx := parsePowerLeafIndex(fields[0]); idx > 0 {
									errRelay = idx
								}
							}
						}
					}
					m.notifyWaitersLocked(plug.Topic, errRelay, stateEvent{
						Err: "Tasmota rejected command (Command: Error — check relay index)",
					})
					return nil, ""
				}
			}

			applyKey := func(relayIdx int, key string) {
				rawVal, exists := rawMap[key]
				if !exists {
					return
				}
				var valStr string
				if json.Unmarshal(rawVal, &valStr) != nil {
					return
				}
				if norm, valid := plug.normalizePowerValue(valStr); valid {
					plug.setRelayState(relayIdx, norm)
					plug.LastError = ""
					plug.UpdatedAt = &now
					m.notifyWaitersLocked(plug.Topic, relayIdx, stateEvent{State: norm})
				}
			}

			applyKey(1, "POWER")
			for idx := 1; idx <= 8; idx++ {
				applyKey(idx, fmt.Sprintf("POWER%d", idx))
			}
		} else if relayIdx := parsePowerLeafIndex(leaf); relayIdx > 0 {
			if norm, valid := plug.normalizePowerValue(payload); valid {
				plug.setRelayState(relayIdx, norm)
				plug.LastError = ""
				plug.UpdatedAt = &now
				m.notifyWaitersLocked(plug.Topic, relayIdx, stateEvent{State: norm})
			}
		}
	}

	return nil, queryTopic
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
	if topic == "" {
		return
	}
	m.mu.RLock()
	c := m.mqttClient
	up := m.mqttUp
	if c == nil || !up {
		m.mu.RUnlock()
		return
	}
	plug := m.plugs[topic]
	maxRelay := 1
	if plug != nil && plug.RelayCount > maxRelay {
		maxRelay = plug.RelayCount
	}
	for _, cfg := range m.configs {
		if cfg.TasmotaTopic == topic && cfg.relayIndex() > maxRelay {
			maxRelay = cfg.relayIndex()
		}
	}
	if maxRelay > 8 {
		maxRelay = 8
	}
	cmdTopics := make([]string, 0, maxRelay+1)
	for r := 1; r <= maxRelay; r++ {
		leaf := fmt.Sprintf("POWER%d", r)
		if plug != nil {
			cmdTopics = append(cmdTopics, plug.formatTopic(0, leaf))
		} else {
			cmdTopics = append(cmdTopics, "cmnd/"+topic+"/"+leaf)
		}
	}
	statusTopic := "cmnd/" + topic + "/Status"
	if plug != nil {
		statusTopic = plug.formatTopic(0, "Status")
	}
	m.mu.RUnlock()

	for _, ct := range cmdTopics {
		c.Publish(ct, 1, false, "")
	}
	c.Publish(statusTopic, 1, false, "8")
}

func (m *module) queryAllKnownPlugs() {
	m.mu.RLock()
	seen := make(map[string]struct{})
	var topics []string
	var driverConfigs []DeviceConfig
	for topic := range m.plugs {
		if topic != "" && !isDriverPrefixedKey(topic) {
			seen[topic] = struct{}{}
			topics = append(topics, topic)
		}
	}
	for _, cfg := range m.configs {
		d := cfg.effectiveDriver()
		if d == DriverTasmota && cfg.TasmotaTopic != "" {
			if _, ok := seen[cfg.TasmotaTopic]; !ok {
				seen[cfg.TasmotaTopic] = struct{}{}
				topics = append(topics, cfg.TasmotaTopic)
			}
		} else if d == DriverShellyGen2 || d == DriverZigbee2MQTT {
			driverConfigs = append(driverConfigs, cfg)
		}
	}
	m.mu.RUnlock()

	for _, t := range topics {
		m.queryPlugState(t)
	}
	for _, cfg := range driverConfigs {
		m.queryDriverState(cfg)
	}
}

func (m *module) pollAssignedPlugsEnergy() {
	m.mu.RLock()
	c := m.mqttClient
	up := m.mqttUp
	var statusPubs []string
	var unknownPubs []string
	var pollConfigs []DeviceConfig
	var queryDriverConfigs []DeviceConfig
	seenStatus := make(map[string]struct{})
	seenUnknown := make(map[string]struct{})
	seenPoll := make(map[string]struct{})
	seenQuery := make(map[string]struct{})
	for _, cfg := range m.configs {
		if !cfg.configured() {
			continue
		}
		d := cfg.effectiveDriver()
		switch d {
		case DriverTasmotaHTTP, DriverShellyHTTP, DriverHomeAssistant:
			key := cfg.plugKey()
			if _, dup := seenPoll[key]; !dup {
				seenPoll[key] = struct{}{}
				pollConfigs = append(pollConfigs, cfg)
			}
			continue
		case DriverShellyGen2, DriverZigbee2MQTT:
			if c != nil && up {
				key := cfg.plugKey()
				if _, dup := seenQuery[key]; !dup {
					seenQuery[key] = struct{}{}
					queryDriverConfigs = append(queryDriverConfigs, cfg)
				}
			}
			continue
		case DriverTasmota:
			// handled below
		default:
			// Shelly Gen1, ESPHome, OpenBeken, Generic MQTT push telemetry on MQTT
			continue
		}
		if c == nil || !up {
			continue
		}
		t := cfg.TasmotaTopic
		plug := m.plugs[t]
		if _, dup := seenStatus[t]; !dup {
			seenStatus[t] = struct{}{}
			if plug != nil {
				statusPubs = append(statusPubs, plug.formatTopic(0, "Status"))
			} else {
				statusPubs = append(statusPubs, "cmnd/"+t+"/Status")
			}
		}
		relayIdx := cfg.relayIndex()
		key := fmt.Sprintf("%s#%d", t, relayIdx)
		if _, dup := seenUnknown[key]; !dup && (plug == nil || plug.relayState(relayIdx) == "UNKNOWN") {
			seenUnknown[key] = struct{}{}
			leaf := fmt.Sprintf("POWER%d", relayIdx)
			if plug != nil {
				unknownPubs = append(unknownPubs, plug.formatTopic(0, leaf))
			} else {
				unknownPubs = append(unknownPubs, "cmnd/"+t+"/"+leaf)
			}
		}
	}
	m.mu.RUnlock()

	for _, pub := range unknownPubs {
		c.Publish(pub, 1, false, "")
	}
	for _, pub := range statusPubs {
		c.Publish(pub, 1, false, "8")
	}
	for _, cfg := range queryDriverConfigs {
		m.queryDriverState(cfg)
	}
	if len(pollConfigs) > 0 {
		var wg sync.WaitGroup
		for _, cfg := range pollConfigs {
			wg.Add(1)
			go func(pc DeviceConfig) {
				defer wg.Done()
				switch pc.effectiveDriver() {
				case DriverTasmotaHTTP:
					_ = m.pollHTTPPlug(context.Background(), pc)
				case DriverShellyHTTP:
					_ = m.pollShellyHTTPPlug(context.Background(), pc)
				case DriverHomeAssistant:
					_ = m.pollHomeAssistantPlug(context.Background(), pc)
				}
			}(cfg)
		}
		wg.Wait()
	}
}

func (m *module) switchPlugVerified(ctx context.Context, topic string, relayIndex int, wantOn bool) error {
	relayIndex = normalizeRelayIndex(relayIndex)
	wantState := "OFF"
	payload := "0"
	if wantOn {
		wantState = "ON"
		payload = "1"
	}

	m.mu.Lock()
	c := m.mqttClient
	up := m.mqttUp
	if c == nil || !up {
		m.mu.Unlock()
		return errBrokerOffline
	}
	plug := m.plugs[topic]
	if plug != nil && plug.Online != nil && !*plug.Online {
		m.mu.Unlock()
		return errors.New("smart plug is offline (LWT Offline)")
	}
	cmdLeaf := fmt.Sprintf("POWER%d", relayIndex)
	cmdTopic := "cmnd/" + topic + "/" + cmdLeaf
	if plug != nil {
		cmdTopic = plug.formatTopic(0, cmdLeaf)
	}
	waiter := &stateWaiter{
		topic: topic,
		relay: relayIndex,
		ch:    make(chan stateEvent, 8),
	}
	m.waiters = append(m.waiters, waiter)
	m.mu.Unlock()
	defer m.removeWaiter(waiter)

	tok := c.Publish(cmdTopic, 1, false, payload)
	if !tok.WaitTimeout(3 * time.Second) {
		return errors.New("MQTT publish timed out")
	}
	if err := tok.Error(); err != nil {
		return err
	}

	overallTimer := time.NewTimer(2500 * time.Millisecond)
	defer overallTimer.Stop()

	var mismatchTimer *time.Timer
	var mismatchC <-chan time.Time
	defer func() {
		if mismatchTimer != nil {
			mismatchTimer.Stop()
		}
	}()
	lastMismatch := ""

	for {
		select {
		case evt := <-waiter.ch:
			if evt.Err != "" {
				return errors.New(evt.Err)
			}
			if evt.State == wantState {
				return nil
			}
			if evt.State != "" && evt.State != wantState {
				lastMismatch = evt.State
				if mismatchTimer == nil {
					// Allow a brief grace window in case an in-flight tele/STATE arrived right before the command reply.
					mismatchTimer = time.NewTimer(450 * time.Millisecond)
					mismatchC = mismatchTimer.C
				}
			}
		case <-mismatchC:
			return fmt.Errorf("plug refused to switch (reported %s; check PowerLock or PowerOnState)", lastMismatch)
		case <-overallTimer.C:
			if lastMismatch != "" {
				return fmt.Errorf("plug refused to switch (reported %s; check PowerLock or PowerOnState)", lastMismatch)
			}
			return errors.New("plug did not confirm state change within 2.5s")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (m *module) getHTTPClient() *http.Client {
	if m.httpClient != nil {
		return m.httpClient
	}
	return &http.Client{Timeout: 5 * time.Second}
}

func (m *module) tasmotaHTTPGet(ctx context.Context, cfg DeviceConfig, cmnd string) ([]byte, error) {
	base := cfg.httpBaseURL()
	if base == "" {
		return nil, errors.New("HTTP plug host or IP is required")
	}
	u, err := url.Parse(base + "/cm")
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if cfg.HTTPPassword != "" {
		user := strings.TrimSpace(cfg.HTTPUser)
		if user == "" {
			user = "admin"
		}
		q.Set("user", user)
		q.Set("password", cfg.HTTPPassword)
	}
	q.Set("cmnd", cmnd)
	u.RawQuery = q.Encode()

	reqCtx := ctx
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		reqCtx, cancel = context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.getHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, errors.New("HTTP authentication failed (check Web Password)")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("plug returned HTTP %d", resp.StatusCode)
	}

	var warnCheck struct {
		Warning string `json:"WARNING"`
	}
	if json.Unmarshal(body, &warnCheck) == nil && warnCheck.Warning != "" {
		return nil, fmt.Errorf("plug auth error: %s", warnCheck.Warning)
	}
	return body, nil
}

func (m *module) pollHTTPPlug(ctx context.Context, cfg DeviceConfig) error {
	key := cfg.plugKey()
	if key == "" {
		return errors.New("HTTP plug host is required")
	}

	raw, err := m.tasmotaHTTPGet(ctx, cfg, "Status 0")
	if err != nil {
		m.mu.Lock()
		plug := m.plugs[key]
		if plug == nil {
			plug = &PlugTelemetry{Topic: key, FriendlyName: cfg.HTTPHost, Power: "UNKNOWN"}
			m.plugs[key] = plug
		}
		offline := false
		plug.Online = &offline
		plug.LastError = err.Error()
		m.mu.Unlock()
		return err
	}

	var parsed struct {
		Status struct {
			DeviceName   string   `json:"DeviceName"`
			FriendlyName []string `json:"FriendlyName"`
		} `json:"Status"`
		StatusFWR struct {
			Hardware string `json:"Hardware"`
		} `json:"StatusFWR"`
		StatusSNS struct {
			ENERGY *energyPayload `json:"ENERGY"`
		} `json:"StatusSNS"`
		StatusSTS map[string]json.RawMessage `json:"StatusSTS"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("invalid Status 0 JSON from plug: %w", err)
	}

	now := time.Now()
	online := true
	m.mu.Lock()
	plug := m.plugs[key]
	if plug == nil {
		plug = &PlugTelemetry{Topic: key, Power: "UNKNOWN"}
		m.plugs[key] = plug
	}
	plug.Topic = key
	plug.Online = &online
	plug.LastError = ""
	plug.UpdatedAt = &now

	if parsed.StatusFWR.Hardware != "" && plug.Model == "" {
		plug.Model = parsed.StatusFWR.Hardware
	}
	if len(parsed.Status.FriendlyName) > 0 {
		plug.RelayNames = make([]string, len(parsed.Status.FriendlyName))
		copy(plug.RelayNames, parsed.Status.FriendlyName)
		if parsed.Status.FriendlyName[0] != "" {
			plug.FriendlyName = parsed.Status.FriendlyName[0]
		}
	}
	if plug.FriendlyName == "" && parsed.Status.DeviceName != "" {
		plug.FriendlyName = parsed.Status.DeviceName
	}
	if plug.FriendlyName == "" {
		plug.FriendlyName = cfg.HTTPHost
	}

	maxRelay := plug.RelayCount
	for k, v := range parsed.StatusSTS {
		idx := parsePowerLeafIndex(k)
		if idx <= 0 {
			continue
		}
		var stateVal string
		if json.Unmarshal(v, &stateVal) != nil {
			continue
		}
		if norm, ok := plug.normalizePowerValue(stateVal); ok {
			plug.setRelayState(idx, norm)
			if idx > maxRelay {
				maxRelay = idx
			}
		}
	}
	if len(parsed.Status.FriendlyName) > maxRelay {
		maxRelay = len(parsed.Status.FriendlyName)
	}
	if maxRelay > 0 {
		plug.RelayCount = maxRelay
	}
	if parsed.StatusSNS.ENERGY != nil {
		applyEnergyReading(plug, parsed.StatusSNS.ENERGY, now)
	}
	m.mu.Unlock()
	return nil
}

func (m *module) switchHTTPPlugVerified(ctx context.Context, cfg DeviceConfig, relayIndex int, wantOn bool) error {
	key := cfg.plugKey()
	if key == "" {
		return errors.New("HTTP plug host is required")
	}
	relayIndex = normalizeRelayIndex(relayIndex)
	wantState := "OFF"
	cmdVal := "0"
	if wantOn {
		wantState = "ON"
		cmdVal = "1"
	}

	raw, err := m.tasmotaHTTPGet(ctx, cfg, fmt.Sprintf("POWER%d %s", relayIndex, cmdVal))
	if err != nil {
		return err
	}

	var respMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &respMap); err != nil {
		return fmt.Errorf("invalid HTTP response from plug: %w", err)
	}
	if cmdRaw, ok := respMap["Command"]; ok {
		var cmdStatus string
		if json.Unmarshal(cmdRaw, &cmdStatus) == nil &&
			(strings.EqualFold(cmdStatus, "Error") || strings.EqualFold(cmdStatus, "Unknown")) {
			return fmt.Errorf("plug rejected command POWER%d (check Relay index)", relayIndex)
		}
	}

	now := time.Now()
	online := true
	m.mu.Lock()
	plug := m.plugs[key]
	if plug == nil {
		plug = &PlugTelemetry{Topic: key, FriendlyName: cfg.HTTPHost, Power: "UNKNOWN"}
		m.plugs[key] = plug
	}

	matchedState := ""
	for k, v := range respMap {
		idx := parsePowerLeafIndex(k)
		if idx != relayIndex {
			continue
		}
		var stateVal string
		if json.Unmarshal(v, &stateVal) != nil {
			continue
		}
		if norm, ok := plug.normalizePowerValue(stateVal); ok {
			plug.setRelayState(idx, norm)
			matchedState = norm
		}
	}
	if matchedState != "" {
		plug.Online = &online
		plug.LastError = ""
		plug.UpdatedAt = &now
	}
	m.mu.Unlock()

	if matchedState == wantState {
		return nil
	}
	if matchedState != "" {
		return fmt.Errorf("plug refused to switch (reported %s; check PowerLock or PowerOnState)", matchedState)
	}
	return errors.New("plug did not confirm state change in HTTP response")
}

func (m *module) switchDevicePower(ctx context.Context, cfg DeviceConfig, wantOn bool) error {
	switch cfg.effectiveDriver() {
	case DriverTasmotaHTTP:
		return m.switchHTTPPlugVerified(ctx, cfg, cfg.relayIndex(), wantOn)
	case DriverShellyHTTP:
		return m.switchShellyHTTPPlugVerified(ctx, cfg, wantOn)
	case DriverHomeAssistant:
		return m.switchHomeAssistantPlugVerified(ctx, cfg, wantOn)
	case DriverTasmota:
		return m.switchPlugVerified(ctx, cfg.TasmotaTopic, cfg.relayIndex(), wantOn)
	default:
		return m.switchMQTTDriverVerified(ctx, cfg, wantOn)
	}
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

func (m *module) updateDeviceUsage(deviceID string, cfg DeviceConfig, safety printerSafety) (dirtyDisk bool, active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p := m.plugs[cfg.plugKey()]
	if p == nil {
		return false, false
	}

	relayPower := p.relayState(cfg.relayIndex())
	plugOn := relayPower == "ON"
	plugOff := relayPower == "OFF"
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
		u.History = trimHistory(u.History, now, powerHistoryKeep)
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
	ticker := time.NewTicker(time.Duration(pollIntervalSec) * time.Second)
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

// outcomeGrace is how long a print with no recorded outcome is given before
// it counts as finished.
const outcomeGrace = 15 * time.Second

// printEnd is how a print ended, as far as it is known.
type printEnd int

const (
	// endUnknown is a print the printer and the server say nothing about yet.
	endUnknown printEnd = iota
	endFinished
	// endNotFinished is a print that was cancelled or that failed.
	endNotFinished
)

// printEnd says how the print jobID ended, and for one that did not finish
// whether it was cancelled or failed. The printer's own state is read first.
// The server's record of the last print is read only when it is that print,
// or when the printer never named its print.
func (s printerSafety) printEnd(jobID string) (printEnd, string) {
	switch s.PrinterState {
	case "FINISHED":
		return endFinished, ""
	case "STOPPED":
		return endNotFinished, "cancelled"
	case "ERROR":
		return endNotFinished, "failed"
	}

	if jobID != "" && s.LastJobID != jobID {
		return endUnknown, ""
	}

	switch strings.ToLower(s.LastJobOutcome) {
	case "finished":
		return endFinished, ""
	case "cancelled", "failed":
		return endNotFinished, strings.ToLower(s.LastJobOutcome)
	}

	return endUnknown, ""
}

// printing reports whether the printer runs a print. Heating or moving by hand
// keeps a printer busy and is not a print.
func (s printerSafety) printing() bool {
	return s.ActiveJob || s.PrinterState == "PRINTING" || s.PrinterState == "PAUSED"
}

// autoOffDue reports whether a printer armed to turn off after its print has
// finished that print and waited out cooldown. It turns off only after a print
// it saw, or one the printer still reports as finished.
func (m *module) autoOffDue(deviceID string, safety printerSafety, cooldown time.Duration, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	tr, ok := m.trackers[deviceID]
	if !ok {
		tr = &autoOffTracker{}
		m.trackers[deviceID] = tr
	}

	// A printer that is away says nothing about its print, so a lost
	// connection never cuts the power mid-print. A print already seen to be
	// over stays over: the wait that began then runs on.
	if !safety.Connected {
		return tr.finishedAt != nil && now.Sub(*tr.finishedAt) >= cooldown
	}

	if !safety.CanTurnOff {
		if safety.printing() {
			tr.sawActive = true
			tr.kept = ""
			tr.offFault = ""
			if safety.JobID != "" {
				tr.jobID = safety.JobID
			}
		}
		tr.finishedAt = nil
		return false
	}

	if !tr.sawActive && (tr.fired || safety.PrinterState != "FINISHED") {
		return false
	}

	// Only a print that finished turns the printer off. After a cancelled or
	// a failed print it stays on, and waits for the next print.
	how, reason := safety.printEnd(tr.jobID)
	if how == endNotFinished {
		*tr = autoOffTracker{fired: true, kept: reason}
		return false
	}

	if tr.finishedAt == nil {
		at := now
		tr.finishedAt = &at
	}

	// The server records how a print ended a moment after the printer stops.
	// Until it has, the wait is at least outcomeGrace, so a short wait does
	// not turn off a printer whose print turns out to be cancelled.
	wait := cooldown
	if how == endUnknown && wait < outcomeGrace {
		wait = outcomeGrace
	}

	return now.Sub(*tr.finishedAt) >= wait
}

// noteAutoOffFault keeps why the plug could not be turned off after a print,
// for the printer's page to show while the turn-off is tried again.
func (m *module) noteAutoOffFault(deviceID string, err error) {
	m.mu.Lock()
	if tr, ok := m.trackers[deviceID]; ok {
		tr.offFault = err.Error()
	}
	m.mu.Unlock()
}

// plugIsOff reports whether the printer's plug is known to be off. A plug
// nothing was heard from is not known to be off.
func (m *module) plugIsOff(cfg DeviceConfig) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	plug, ok := m.plugs[cfg.plugKey()]

	return ok && plug.relayState(cfg.relayIndex()) == "OFF"
}

// forgetPrint drops what was seen of a printer's print. The printer then
// turns off only after a print it is seen running from here on.
func (m *module) forgetPrint(deviceID string) {
	m.mu.Lock()
	m.trackers[deviceID] = &autoOffTracker{fired: true}
	m.mu.Unlock()
}

// afterAutoOff is the config once the plug was turned off after a print. The
// choice for one print is used up, so the printer goes back to its default. A
// printer that turns off by default stays armed, and waits to see its next
// print before it acts again.
func (m *module) afterAutoOff(deviceID string, cfg DeviceConfig) DeviceConfig {
	cfg.AutoOffAfterPrint = cfg.AutoOffDefault

	m.mu.Lock()
	if cfg.AutoOffDefault {
		m.trackers[deviceID] = &autoOffTracker{fired: true}
	} else {
		delete(m.trackers, deviceID)
	}
	m.mu.Unlock()

	return cfg
}

// skippedPrintIsOver follows a printer that turns off by default while a
// person has switched that off for one print. It answers true once, when the
// print after the skipped one starts.
func (m *module) skippedPrintIsOver(deviceID string, safety printerSafety) bool {
	if !safety.Connected {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	skip, ok := m.skips[deviceID]
	if !ok {
		skip = &skippedPrint{}
		m.skips[deviceID] = skip
	}

	switch {
	case safety.printing() && skip.ended:
		delete(m.skips, deviceID)
		return true
	case safety.printing():
		skip.sawPrint = true
		if safety.JobID != "" {
			skip.jobID = safety.JobID
		}
	case skip.sawPrint:
		// A skipped print that did not finish is not used up. The choice
		// holds for the print that is started in its place.
		if how, _ := safety.printEnd(skip.jobID); how == endNotFinished {
			*skip = skippedPrint{}
		} else {
			skip.ended = true
		}
	}

	return false
}

func (m *module) stepAutoOff(ctx context.Context) {
	m.pollAssignedPlugsEnergy()

	m.mu.RLock()
	assigned := make(map[string]DeviceConfig)
	for id, cfg := range m.configs {
		if cfg.configured() {
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

		dirty, active := m.updateDeviceUsage(deviceID, cfg, safety)
		anyDirty = anyDirty || dirty
		hasActiveUsage = hasActiveUsage || active

		if !cfg.AutoOffAfterPrint {
			// Switched off for one print on a printer that turns off by
			// default: the default comes back when the print after it starts.
			if cfg.AutoOffDefault && m.skippedPrintIsOver(deviceID, safety) {
				cfg.AutoOffAfterPrint = true
				_ = m.saveConfig(ctx, deviceID, cfg)
			}
			continue
		}

		cooldownSec := defaultCooldown
		if cfg.CooldownSec != nil {
			cooldownSec = *cfg.CooldownSec
		}

		// A plug that is already off has nothing left to turn off. Whatever
		// print was followed is over, and the next one starts from nothing.
		if m.plugIsOff(cfg) {
			m.forgetPrint(deviceID)
			continue
		}

		if !m.autoOffDue(deviceID, safety, time.Duration(cooldownSec)*time.Second, time.Now()) {
			continue
		}

		recheck, err := m.checkSafety(ctx, "", deviceID)
		if err != nil || !recheck.CanTurnOff {
			continue
		}

		if err := m.switchDevicePower(ctx, cfg, false); err != nil {
			m.noteAutoOffFault(deviceID, err)
			fmt.Fprintf(os.Stderr, "%s: auto-off failed for printer %q [%s]: %v\n", logName, safety.DeviceName, deviceID, err)
			continue
		}

		fmt.Fprintf(os.Stderr, "%s: auto-off triggered for printer %q [%s] (plug %s relay %d)\n", logName, safety.DeviceName, deviceID, cfg.plugKey(), cfg.relayIndex())
		cfg = m.afterAutoOff(deviceID, cfg)
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
	Settings           Settings `json:"settings"`
	MQTTConfigured     bool     `json:"mqtt_configured"`
	MQTTConnected      bool     `json:"mqtt_connected"`
	MQTTBroker         string   `json:"mqtt_broker,omitempty"`
	MQTTError          string   `json:"mqtt_error,omitempty"`
	MQTTWarning        string   `json:"mqtt_warning,omitempty"`
	MQTTBuiltinRunning bool     `json:"mqtt_builtin_running"`
	MQTTBuiltinPort    int      `json:"mqtt_builtin_port,omitempty"`
	MQTTBuiltinClients int      `json:"mqtt_builtin_clients"`
	// MQTTBuiltinGranted says whether the operator approved the built-in
	// broker's port. MQTTBuiltinPort is that port, running or not.
	MQTTBuiltinGranted bool `json:"mqtt_builtin_granted"`
	HAConfigured       bool `json:"ha_configured"`
	// A pinned value was written in the host's own settings for this
	// extension, and is read-only here.
	MQTTHostPinned bool `json:"mqtt_host_pinned"`
	HAURLPinned    bool `json:"ha_url_pinned"`
	// SuggestedHTTPHost is the http_host option, offered as the address when a
	// plug is set up by IP address.
	SuggestedHTTPHost string `json:"suggested_http_host,omitempty"`
}

func (m *module) buildSettingsResponse() settingsResponse {
	s := m.settings()
	m.mu.RLock()
	up := m.mqttUp
	broker := m.mqttBroker
	errMsg := m.mqttError
	warnMsg := m.mqttWarning
	brk := m.embeddedBroker
	grantedPort := m.builtinPort
	hostPinned := m.pinnedMQTTHost != ""
	haPinned := m.pinnedHAURL != ""
	suggestedHTTP := m.suggestedHTTPHost
	m.mu.RUnlock()

	builtinRunning := brk != nil
	builtinPort := grantedPort
	builtinClients := 0
	if brk != nil {
		builtinPort = brk.Port()
		builtinClients = brk.ClientCount()
	}

	return settingsResponse{
		Settings:           s.redacted(),
		MQTTConfigured:     s.mqttConfigured(),
		MQTTConnected:      up,
		MQTTBroker:         broker,
		MQTTError:          errMsg,
		MQTTWarning:        warnMsg,
		MQTTBuiltinRunning: builtinRunning,
		MQTTBuiltinPort:    builtinPort,
		MQTTBuiltinClients: builtinClients,
		MQTTBuiltinGranted: grantedPort > 0,
		HAConfigured:       s.haConfigured(),
		MQTTHostPinned:     hostPinned,
		HAURLPinned:        haPinned,
		SuggestedHTTPHost:  suggestedHTTP,
	}
}

// powerHistoryKeep is how far back the power samples of a printer reach: the
// longest span the dashboard card draws, the same as the core's temperature
// card. statusHistoryWindow is what the printer's own page draws.
const (
	powerHistoryKeep    = 60 * time.Minute
	statusHistoryWindow = 30 * time.Minute
)

// trimHistory drops the samples older than keep before now, and the oldest
// beyond maxPowerSamples.
func trimHistory(history []PowerSample, now time.Time, keep time.Duration) []PowerSample {
	since := now.Add(-keep).Unix()
	first := 0
	for first < len(history) && history[first].Timestamp < since {
		first++
	}
	history = history[first:]
	if len(history) > maxPowerSamples {
		history = history[len(history)-maxPowerSamples:]
	}
	return history
}

// overviewEntry is one printer's plug in the fleet overview: what a list of
// every printer shows. The graph samples come only when asked for, and the
// print history never.
type overviewEntry struct {
	DeviceID          string            `json:"device_id"`
	Config            DeviceConfig      `json:"config"`
	Plug              *PlugTelemetry    `json:"plug,omitempty"`
	SessionUsage      *PowerSession     `json:"session_usage,omitempty"`
	CurrentPrintUsage *PrintPowerRecord `json:"current_print_usage,omitempty"`
	// PowerHistory is the samples of the last minutes ?history= names, at
	// most an hour. It is an empty list, never left out, when asked for.
	PowerHistory *[]PowerSample `json:"power_history,omitempty"`
	// CanOperate says whether the caller may switch this printer's plug. It
	// only decides what the page offers: the routes ask again.
	CanOperate bool `json:"can_operate"`
}

type overviewResponse struct {
	Devices            []overviewEntry `json:"devices"`
	MQTTConfigured     bool            `json:"mqtt_configured"`
	MQTTConnected      bool            `json:"mqtt_connected"`
	MQTTError          string          `json:"mqtt_error,omitempty"`
	HAConfigured       bool            `json:"ha_configured"`
	DefaultCooldownSec int             `json:"default_cooldown_sec"`
	ConfirmTurnOff     bool            `json:"confirm_turn_off"`
	PollIntervalSec    int             `json:"poll_interval_sec"`
	// CanConfigure says whether the caller may change plugs and
	// connections. It only decides what the page offers.
	CanConfigure bool `json:"can_configure"`
}

// historyAsked reads ?history=, the minutes of samples a graph draws, clamped
// to powerHistoryKeep. Anything else, or nothing, asks for none.
func historyAsked(raw string) time.Duration {
	minutes, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || minutes <= 0 {
		return 0
	}
	asked := time.Duration(minutes) * time.Minute
	return min(asked, powerHistoryKeep)
}

// overviewOptions is what a caller of buildOverview asks for beyond the plugs.
type overviewOptions struct {
	// visible answers whether the caller may see a printer.
	visible func(deviceID string) bool
	// history adds each printer's samples of the last history, none when 0.
	history time.Duration
	// canOperate answers whether the caller may switch a printer's plug;
	// nil answers no.
	canOperate func(deviceID string) bool
}

// plugSnapshotLocked is the plug a printer's config points at, with the state
// of the relay that config names. The caller holds m.mu.
func (m *module) plugSnapshotLocked(cfg DeviceConfig) *PlugTelemetry {
	key := cfg.plugKey()
	if key == "" {
		return nil
	}
	if p, ok := m.plugs[key]; ok {
		cp := p.clone()
		relayIdx := cfg.relayIndex()
		cp.Power = cp.relayState(relayIdx)
		if relayIdx > 1 && len(cp.RelayNames) >= relayIdx && cp.RelayNames[relayIdx-1] != "" {
			cp.FriendlyName = cp.RelayNames[relayIdx-1]
		}
		return &cp
	}
	d := cfg.effectiveDriver()
	friendly := cfg.TasmotaTopic
	switch d {
	case DriverTasmotaHTTP, DriverShellyHTTP:
		friendly = cfg.HTTPHost
	case DriverHomeAssistant:
		friendly = cfg.HAEntityID
	case DriverGenericMQTT:
		friendly = cfg.MQTTCommandTopic
	}
	return &PlugTelemetry{Topic: key, Driver: d, FriendlyName: friendly, Power: "UNKNOWN"}
}

// buildOverview answers every printer that has a plug or an auto-off setting,
// kept to the printers visible answers true for. It reads memory only, so one
// request serves a whole list of printers.
func (m *module) buildOverview(opts overviewOptions) overviewResponse {
	s := m.settings()
	now := time.Now()
	m.mu.RLock()
	entries := make([]overviewEntry, 0, len(m.configs))
	for deviceID, cfg := range m.configs {
		if !opts.visible(deviceID) {
			continue
		}
		entry := overviewEntry{DeviceID: deviceID, Config: cfg.redacted()}
		if cfg.configured() {
			entry.Plug = m.plugSnapshotLocked(cfg)
			if held := m.usage[deviceID]; held != nil {
				// Only the parts the overview answers are copied, so a poll
				// does not copy an hour of samples it does not send.
				usage := cloneDeviceUsage(&DeviceUsage{Session: held.Session, CurrentPrint: held.CurrentPrint})
				entry.SessionUsage = usage.Session
				entry.CurrentPrintUsage = usage.CurrentPrint
				if opts.history > 0 {
					samples := append([]PowerSample{}, trimHistory(held.History, now, opts.history)...)
					entry.PowerHistory = &samples
				}
			}
			if opts.history > 0 && entry.PowerHistory == nil {
				entry.PowerHistory = &[]PowerSample{}
			}
		}
		entries = append(entries, entry)
	}
	up := m.mqttUp
	errMsg := m.mqttError
	m.mu.RUnlock()

	// Asked outside the lock: each answer may be a question to the host.
	if opts.canOperate != nil {
		for i := range entries {
			entries[i].CanOperate = entries[i].Config.configured() && opts.canOperate(entries[i].DeviceID)
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].DeviceID < entries[j].DeviceID })

	return overviewResponse{
		Devices:            entries,
		MQTTConfigured:     s.mqttConfigured(),
		MQTTConnected:      up,
		MQTTError:          errMsg,
		HAConfigured:       s.haConfigured(),
		DefaultCooldownSec: s.AutoOffCooldownSec,
		ConfirmTurnOff:     s.confirmTurnOffEnabled(),
		PollIntervalSec:    pollIntervalSec,
	}
}

// visibleDevices reads the printers principal may see, so a list this module
// answers never names a printer the caller could not open.
func (m *module) visibleDevices(ctx context.Context, principal string) (map[string]bool, error) {
	seen := make(map[string]bool)
	for offset := 0; ; {
		page, err := m.devices.List(ctx, deviceapi.ListParams{
			Principal: principal,
			Limit:     deviceapi.MaxPageLimit,
			Offset:    offset,
		})
		if err != nil {
			return nil, err
		}
		for _, dev := range page.Items {
			seen[dev.ID] = true
		}
		offset += len(page.Items)
		if len(page.Items) == 0 || offset >= page.Total {
			return seen, nil
		}
	}
}

type statusResponse struct {
	DeviceID            string         `json:"device_id"`
	MQTTConfigured      bool           `json:"mqtt_configured"`
	MQTTConnected       bool           `json:"mqtt_connected"`
	MQTTBroker          string         `json:"mqtt_broker,omitempty"`
	MQTTError           string         `json:"mqtt_error,omitempty"`
	HAConfigured        bool           `json:"ha_configured"`
	Config              DeviceConfig   `json:"config"`
	Plug                *PlugTelemetry `json:"plug,omitempty"`
	CanTurnOff          bool           `json:"can_turn_off"`
	LockReason          string         `json:"lock_reason,omitempty"`
	PrinterConnected    bool           `json:"printer_connected"`
	PrinterState        string         `json:"printer_state,omitempty"`
	PrinterMoving       bool           `json:"printer_moving"`
	AutoOffPending      bool           `json:"auto_off_pending"`
	AutoOffRemainingSec int            `json:"auto_off_remaining_sec"`
	// AutoOffError is why the plug could not be turned off after a print.
	// AutoOffKept is why the printer stayed on: cancelled or failed.
	AutoOffError       string             `json:"auto_off_error,omitempty"`
	AutoOffKept        string             `json:"auto_off_kept,omitempty"`
	DefaultCooldownSec int                `json:"default_cooldown_sec"`
	ConfirmTurnOff     bool               `json:"confirm_turn_off"`
	PollIntervalSec    int                `json:"poll_interval_sec"`
	PowerHistory       []PowerSample      `json:"power_history,omitempty"`
	SessionUsage       *PowerSession      `json:"session_usage,omitempty"`
	CurrentPrintUsage  *PrintPowerRecord  `json:"current_print_usage,omitempty"`
	PrintHistory       []PrintPowerRecord `json:"print_history,omitempty"`
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

	if cfg.configured() {
		if dirty, _ := m.updateDeviceUsage(deviceID, cfg, safety); dirty {
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
	plugCopy := m.plugSnapshotLocked(cfg)
	var pending bool
	var remaining int
	var offFault, kept string
	if cfg.AutoOffAfterPrint {
		if tr, ok := m.trackers[deviceID]; ok {
			offFault, kept = tr.offFault, tr.kept
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
	m.mu.RUnlock()

	var (
		historyCopy      []PowerSample
		sessionCopy      *PowerSession
		currentPrintCopy *PrintPowerRecord
		printHistoryCopy []PrintPowerRecord
	)
	if usageCopy != nil {
		historyCopy = trimHistory(usageCopy.History, time.Now(), statusHistoryWindow)
		sessionCopy = usageCopy.Session
		currentPrintCopy = usageCopy.CurrentPrint
		printHistoryCopy = usageCopy.PrintHistory
	}

	return statusResponse{
		DeviceID:            deviceID,
		MQTTConfigured:      s.mqttConfigured(),
		MQTTConnected:       mqttUp,
		MQTTBroker:          mqttBroker,
		MQTTError:           mqttErr,
		HAConfigured:        s.haConfigured(),
		Config:              cfg.redacted(),
		Plug:                plugCopy,
		CanTurnOff:          safety.CanTurnOff,
		LockReason:          safety.LockReason,
		PrinterConnected:    safety.Connected,
		PrinterState:        safety.PrinterState,
		PrinterMoving:       safety.Moving,
		AutoOffPending:      pending,
		AutoOffRemainingSec: remaining,
		AutoOffError:        offFault,
		AutoOffKept:         kept,
		DefaultCooldownSec:  s.AutoOffCooldownSec,
		ConfirmTurnOff:      s.confirmTurnOffEnabled(),
		PollIntervalSec:     pollIntervalSec,
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

	mux.HandleFunc("PUT /settings", m.guard(mayConfigure, func(w http.ResponseWriter, r *http.Request) {
		var body Settings
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			refuse(w, r, InvalidInput.New(err.Error()))
			return
		}
		current := m.settings()
		if body.MQTTPassword == "" && body.KeepPassword {
			body.MQTTPassword = current.MQTTPassword
		}
		if body.HAToken == "" && body.KeepHAToken {
			body.HAToken = current.HAToken
		}
		next := m.applyHostPins(body)
		// A save that turns the built-in broker on or changes its user name
		// or password needs both. An install from before 0.5.0 that runs the
		// built-in broker without them can still save its other settings;
		// the broker stays off and says why until both are set.
		unchanged := current.MQTTBuiltin && current.MQTTUsername == next.MQTTUsername && current.MQTTPassword == next.MQTTPassword
		if next.MQTTBuiltin && !next.builtinCredentialsSet() && !unchanged {
			refuse(w, r, InvalidInput.New(errBuiltinNeedsCredentials))
			return
		}
		if err := m.writeSettingsToDisk(next); err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		m.held.Store(&next)
		m.reconnectMQTT(next)
		// Allow brief time for initial connect so the returned status reflects it if immediate.
		time.Sleep(300 * time.Millisecond)
		answer(w, http.StatusOK, m.buildSettingsResponse())
	}))

	mux.HandleFunc("POST /broker/test", m.guard(mayConfigure, func(w http.ResponseWriter, r *http.Request) {
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
		ok, broker, errMsg, warnMsg := m.testBroker(override)
		answer(w, http.StatusOK, map[string]any{
			"ok":      ok,
			"broker":  broker,
			"error":   errMsg,
			"warning": warnMsg,
		})
	}))

	mux.HandleFunc("POST /ha/test", m.guard(mayConfigure, func(w http.ResponseWriter, r *http.Request) {
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
		ok, haURL, entities, errMsg := m.testHomeAssistant(r.Context(), override)
		answer(w, http.StatusOK, map[string]any{
			"ok":       ok,
			"url":      haURL,
			"entities": entities,
			"error":    errMsg,
		})
	}))

	mux.HandleFunc("POST /discover", m.guard(mayConfigure, func(w http.ResponseWriter, _ *http.Request) {
		m.discoverPlugs()
		m.syncDriverSubscriptions()
		time.Sleep(400 * time.Millisecond)
		m.mu.RLock()
		discovered := m.discoveredListLocked()
		m.mu.RUnlock()
		answer(w, http.StatusOK, map[string]any{
			"plugs": discovered,
		})
	}))

	mux.HandleFunc("GET /assignments", m.guard(mayConfigure, func(w http.ResponseWriter, r *http.Request) {
		s := m.settings()
		m.mu.RLock()
		configsCopy := make(map[string]DeviceConfig, len(m.configs))
		for k, v := range m.configs {
			configsCopy[k] = v.redacted()
		}
		discovered := m.discoveredListLocked()
		up := m.mqttUp
		broker := m.mqttBroker
		errMsg := m.mqttError
		m.mu.RUnlock()
		var haEntities []HAEntity
		if s.haConfigured() {
			haEntities, _ = m.fetchHAEntities(r.Context(), s)
		}
		answer(w, http.StatusOK, map[string]any{
			"assignments":          configsCopy,
			"discovered_plugs":     discovered,
			"mqtt_configured":      s.mqttConfigured(),
			"mqtt_connected":       up,
			"mqtt_broker":          broker,
			"mqtt_error":           errMsg,
			"ha_configured":        s.haConfigured(),
			"ha_entities":          haEntities,
			"default_cooldown_sec": s.AutoOffCooldownSec,
		})
	}))

	mux.HandleFunc("GET /overview", func(w http.ResponseWriter, r *http.Request) {
		seen, err := m.visibleDevices(r.Context(), m.principalOf(r))
		if err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		person := m.principalOf(r)
		resp := m.buildOverview(overviewOptions{
			visible:    func(deviceID string) bool { return seen[deviceID] },
			history:    historyAsked(r.URL.Query().Get("history")),
			canOperate: func(deviceID string) bool { return m.mayHint(r.Context(), person, mayOperate, deviceID) },
		})
		resp.CanConfigure = m.mayHint(r.Context(), person, mayConfigure, "")
		answer(w, http.StatusOK, resp)
	})

	mux.HandleFunc("GET /devices/{device_id}", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("device_id")
		principal := m.principalOf(r)
		resp, err := m.buildStatus(r.Context(), principal, deviceID)
		if err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		answer(w, http.StatusOK, resp)
	})

	mux.HandleFunc("PUT /devices/{device_id}/config", m.guard(mayConfigureDevice, func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("device_id")
		principal := m.principalOf(r)
		var body DeviceConfig
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			refuse(w, r, InvalidInput.New(err.Error()))
			return
		}
		mode := strings.ToLower(strings.TrimSpace(body.Mode))
		if mode != "" && mode != "mqtt" && mode != "http" && mode != "ha" {
			refuse(w, r, InvalidInput.New("mode must be mqtt, http, or ha"))
			return
		}
		if !isValidDriverInput(body.Driver) {
			refuse(w, r, InvalidInput.New("unsupported plug driver"))
			return
		}
		if body.usesHTTP() && strings.TrimSpace(body.HTTPHost) != "" && normalizeHTTPHost(body.HTTPHost) == "" {
			refuse(w, r, InvalidInput.New("invalid HTTP plug IP, hostname, or URL"))
			return
		}
		normalizedCfg := normalizeDeviceConfig(body)
		candidateKey := normalizedCfg.plugKey()
		relayIdx := normalizedCfg.relayIndex()
		if candidateKey != "" {
			m.mu.RLock()
			var conflictID string
			for otherID, otherCfg := range m.configs {
				if otherID != deviceID && otherCfg.configured() && otherCfg.plugKey() == candidateKey && otherCfg.relayIndex() == relayIdx {
					conflictID = otherID
					break
				}
			}
			m.mu.RUnlock()
			if conflictID != "" {
				refuse(w, r, TopicInUse.New("plug relay is already assigned to another printer").
					With(fault.StringParam("topic", candidateKey), fault.StringParam("device", conflictID)))
				return
			}
		}
		if err := m.saveConfig(r.Context(), deviceID, body); err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		if candidateKey != "" {
			savedCfg, _ := m.getConfig(r.Context(), deviceID)
			switch savedCfg.effectiveDriver() {
			case DriverTasmotaHTTP:
				_ = m.pollHTTPPlug(r.Context(), savedCfg)
			case DriverShellyHTTP:
				_ = m.pollShellyHTTPPlug(r.Context(), savedCfg)
			case DriverHomeAssistant:
				_ = m.pollHomeAssistantPlug(r.Context(), savedCfg)
			}
		}
		resp, err := m.buildStatus(r.Context(), principal, deviceID)
		if err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		answer(w, http.StatusOK, resp)
	}))

	mux.HandleFunc("POST /devices/{device_id}/power", m.guard(mayOperate, func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("device_id")
		principal := m.principalOf(r)
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
		if !cfg.configured() {
			refuse(w, r, NotConfigured.New("no smart plug is set up for this printer"))
			return
		}

		if action == "toggle" {
			m.mu.RLock()
			p := m.plugs[cfg.plugKey()]
			cur := ""
			if p != nil {
				cur = p.relayState(cfg.relayIndex())
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

		if err := m.switchDevicePower(r.Context(), cfg, action == "on"); err != nil {
			if errors.Is(err, errBrokerOffline) {
				refuse(w, r, BrokerOffline.New(err.Error()))
			} else {
				refuse(w, r, PlugRefused.New(err.Error()))
			}
			return
		}

		go func(c DeviceConfig) {
			time.Sleep(400 * time.Millisecond)
			m.refreshConfigState(context.Background(), c)
		}(cfg)

		resp, err := m.buildStatus(r.Context(), principal, deviceID)
		if err != nil {
			refuse(w, r, InternalError.New(err.Error()))
			return
		}
		answer(w, http.StatusOK, resp)
	}))

	mux.HandleFunc("POST /devices/{device_id}/auto-off", m.guard(mayOperate, func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("device_id")
		principal := m.principalOf(r)
		var body struct {
			Enabled bool `json:"enabled"`
			// Default, when sent, sets what the printer does after every
			// print, and the print that runs now follows it.
			Default *bool `json:"default"`
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
		if !cfg.configured() {
			refuse(w, r, NotConfigured.New("no smart plug is set up for this printer"))
			return
		}
		cfg.AutoOffAfterPrint = body.Enabled
		if body.Default != nil {
			cfg.AutoOffDefault = *body.Default
			cfg.AutoOffAfterPrint = *body.Default
		}
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
	}))

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
