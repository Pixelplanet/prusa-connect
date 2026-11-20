package main

import (
	"bufio"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	mqttPktConnect     = 1
	mqttPktConnack     = 2
	mqttPktPublish     = 3
	mqttPktPuback      = 4
	mqttPktPubrec      = 5
	mqttPktPubrel      = 6
	mqttPktPubcomp     = 7
	mqttPktSubscribe   = 8
	mqttPktSuback      = 9
	mqttPktUnsubscribe = 10
	mqttPktUnsuback    = 11
	mqttPktPingreq     = 12
	mqttPktPingresp    = 13
	mqttPktDisconnect  = 14

	maxMQTTPacketBytes = 1024 * 1024 // 1 MiB safety limit for embedded plug broker
)

type retainedMessage struct {
	topic   string
	payload []byte
}

type brokerSubscription struct {
	filter string
	qos    byte
}

// EmbeddedBroker is a lightweight pure-Go MQTT 3.1/3.1.1 broker for users
// running Tasmota Power Control without an external broker such as Mosquitto.
type EmbeddedBroker struct {
	listener net.Listener
	port     int
	username string
	password string

	mu       sync.RWMutex
	closed   bool
	clients  map[*brokerClient]struct{}
	byID     map[string]*brokerClient
	retained map[string]retainedMessage
	nextSeq  atomic.Uint64
	accepted atomic.Uint64
	wg       sync.WaitGroup
}

type brokerClient struct {
	broker    *EmbeddedBroker
	conn      net.Conn
	reader    *bufio.Reader
	id        string
	keepAlive time.Duration

	willTopic   string
	willPayload []byte
	willQoS     byte
	willRetain  bool
	hasWill     bool

	subMu       sync.RWMutex
	subs        map[string]byte
	inboundQoS2 map[uint16]struct{}
	nextPID     atomic.Uint32

	sendCh    chan []byte
	done      chan struct{}
	closeOnce sync.Once
	graceful  atomic.Bool
}

// errBrokerNeedsCredentials refuses a built-in broker without a user name
// and a password: it would let anyone on the network switch the plugs.
var errBrokerNeedsCredentials = errors.New("the built-in broker needs a user name and a password")

// StartEmbeddedBroker binds a TCP listener on listenAddr and serves MQTT
// 3.1/3.1.1 clients that sign in with username and password. It refuses to
// start without both, so the broker never accepts an anonymous client.
func StartEmbeddedBroker(listenAddr, username, password string) (*EmbeddedBroker, error) {
	if strings.TrimSpace(username) == "" || password == "" {
		return nil, errBrokerNeedsCredentials
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, err
	}
	port := 0
	if tcpAddr, ok := ln.Addr().(*net.TCPAddr); ok {
		port = tcpAddr.Port
	} else if _, portStr, splitErr := net.SplitHostPort(ln.Addr().String()); splitErr == nil {
		port, _ = strconv.Atoi(portStr)
	}

	b := &EmbeddedBroker{
		listener: ln,
		port:     port,
		username: username,
		password: password,
		clients:  make(map[*brokerClient]struct{}),
		byID:     make(map[string]*brokerClient),
		retained: make(map[string]retainedMessage),
	}

	b.wg.Add(1)
	go b.acceptLoop()
	return b, nil
}

// Port returns the bound TCP port.
func (b *EmbeddedBroker) Port() int {
	if b == nil {
		return 0
	}
	return b.port
}

// ClientCount returns the number of currently authenticated connected MQTT clients.
func (b *EmbeddedBroker) ClientCount() int {
	if b == nil {
		return 0
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.clients)
}

// Pipe connects a client in this process to the broker over an in-memory
// pipe. The module's own MQTT client uses it, so it reaches the built-in
// broker without opening a socket. The client signs in like any other.
func (b *EmbeddedBroker) Pipe() (net.Conn, error) {
	if b == nil {
		return nil, net.ErrClosed
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, net.ErrClosed
	}
	b.wg.Add(1)
	b.mu.Unlock()

	server, client := net.Pipe()
	go b.handleConn(server)
	return client, nil
}

// signsIn answers whether username and password are the broker's own.
func (b *EmbeddedBroker) signsIn(username, password string) bool {
	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(b.username)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(password), []byte(b.password)) == 1
	return userOK && passOK
}

// AcceptedCount returns the total number of TCP connections accepted by this broker listener.
func (b *EmbeddedBroker) AcceptedCount() uint64 {
	if b == nil {
		return 0
	}
	return b.accepted.Load()
}

// Close shuts down the listener and disconnects all active clients.
func (b *EmbeddedBroker) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	clients := make([]*brokerClient, 0, len(b.clients))
	for c := range b.clients {
		clients = append(clients, c)
	}
	b.mu.Unlock()

	err := b.listener.Close()
	for _, c := range clients {
		c.graceful.Store(true) // avoid firing LWT on intentional broker shutdown
		c.close()
	}
	b.wg.Wait()
	return err
}

func (b *EmbeddedBroker) acceptLoop() {
	defer b.wg.Done()
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			b.mu.RLock()
			closed := b.closed
			b.mu.RUnlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}

		b.accepted.Add(1)
		b.wg.Add(1)
		go b.handleConn(conn)
	}
}

func (b *EmbeddedBroker) handleConn(conn net.Conn) {
	defer b.wg.Done()

	reader := bufio.NewReaderSize(conn, 16*1024)
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))

	pktType, flags, body, err := readMQTTPacket(reader)
	if err != nil || pktType != mqttPktConnect || (flags&0x0F) != 0 {
		_ = conn.Close()
		return
	}

	c, rc := b.parseConnect(conn, reader, body)
	if rc != 0 {
		_ = writeMQTTFrame(conn, []byte{mqttPktConnack << 4, 0x02, 0x00, rc})
		_ = conn.Close()
		return
	}

	var replaced *brokerClient
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		_ = conn.Close()
		return
	}
	if prev, ok := b.byID[c.id]; ok && prev != c {
		replaced = prev
	}
	b.clients[c] = struct{}{}
	b.byID[c.id] = c
	b.mu.Unlock()

	if replaced != nil {
		// MQTT-3.1.4-2: server must disconnect existing client with same ClientID.
		replaced.graceful.Store(true)
		replaced.close()
	}

	if err := writeMQTTFrame(conn, []byte{mqttPktConnack << 4, 0x02, 0x00, 0x00}); err != nil {
		b.unregisterClient(c, false)
		c.close()
		return
	}

	b.wg.Add(1)
	go c.writeLoop()

	c.readLoop()
	b.unregisterClient(c, !c.graceful.Load())
	c.close()
}

func (b *EmbeddedBroker) parseConnect(conn net.Conn, reader *bufio.Reader, body []byte) (*brokerClient, byte) {
	pos := 0
	protoName, n, err := readMQTTString(body[pos:])
	if err != nil {
		return nil, 0x01
	}
	pos += n
	if pos+4 > len(body) {
		return nil, 0x01
	}
	protoLevel := body[pos]
	pos++
	if !((protoName == "MQTT" && protoLevel == 4) || (protoName == "MQIsdp" && protoLevel == 3)) {
		return nil, 0x01 // unacceptable protocol level
	}

	connFlags := body[pos]
	pos++
	if connFlags&0x01 != 0 {
		// Reserved bit must be 0 in MQTT 3.1.1; reject malformed connect flags.
		if protoLevel == 4 {
			return nil, 0x01
		}
	}
	keepAliveSec := binary.BigEndian.Uint16(body[pos : pos+2])
	pos += 2

	clientID, n, err := readMQTTString(body[pos:])
	if err != nil {
		return nil, 0x02
	}
	pos += n

	cleanSession := (connFlags & 0x02) != 0
	if clientID == "" {
		if protoLevel == 4 && cleanSession {
			clientID = fmt.Sprintf("auto-%d", b.nextSeq.Add(1))
		} else {
			return nil, 0x02 // identifier rejected
		}
	}

	willFlag := (connFlags & 0x04) != 0
	willQoS := (connFlags >> 3) & 0x03
	willRetain := (connFlags & 0x20) != 0
	if !willFlag && (willQoS != 0 || willRetain) {
		return nil, 0x01
	}
	if willQoS > 2 {
		return nil, 0x01
	}

	var willTopic string
	var willPayload []byte
	if willFlag {
		willTopic, n, err = readMQTTString(body[pos:])
		if err != nil || willTopic == "" {
			return nil, 0x01
		}
		pos += n
		willPayload, n, err = readMQTTBytes(body[pos:])
		if err != nil {
			return nil, 0x01
		}
		pos += n
	}

	userFlag := (connFlags & 0x80) != 0
	passFlag := (connFlags & 0x40) != 0
	if passFlag && !userFlag && protoLevel == 4 {
		return nil, 0x01
	}

	var user, pass string
	if userFlag {
		user, n, err = readMQTTString(body[pos:])
		if err != nil {
			return nil, 0x01
		}
		pos += n
	}
	if passFlag {
		var passBytes []byte
		passBytes, n, err = readMQTTBytes(body[pos:])
		if err != nil {
			return nil, 0x01
		}
		pass = string(passBytes)
		pos += n
	}

	// Every client signs in. StartEmbeddedBroker refuses a broker without
	// a user name and a password, so there is no anonymous case.
	if !userFlag || !passFlag || !b.signsIn(user, pass) {
		return nil, 0x04 // bad username or password
	}

	c := &brokerClient{
		broker:      b,
		conn:        conn,
		reader:      reader,
		id:          clientID,
		keepAlive:   time.Duration(keepAliveSec) * time.Second,
		willTopic:   willTopic,
		willPayload: willPayload,
		willQoS:     willQoS,
		willRetain:  willRetain,
		hasWill:     willFlag,
		subs:        make(map[string]byte),
		inboundQoS2: make(map[uint16]struct{}),
		sendCh:      make(chan []byte, 512),
		done:        make(chan struct{}),
	}
	return c, 0x00
}

func (b *EmbeddedBroker) unregisterClient(c *brokerClient, fireWill bool) {
	b.mu.Lock()
	delete(b.clients, c)
	if cur, ok := b.byID[c.id]; ok && cur == c {
		delete(b.byID, c.id)
	}
	brokerClosed := b.closed
	b.mu.Unlock()

	if fireWill && !brokerClosed && c.hasWill && c.willTopic != "" {
		b.publish(c.willTopic, c.willPayload, c.willQoS, c.willRetain)
	}
}

func (c *brokerClient) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

func (c *brokerClient) enqueue(pkt []byte) bool {
	select {
	case c.sendCh <- pkt:
		return true
	case <-c.done:
		return false
	default:
	}

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case c.sendCh <- pkt:
		return true
	case <-c.done:
		return false
	case <-timer.C:
		return false
	}
}

func (c *brokerClient) writeLoop() {
	defer c.broker.wg.Done()
	for {
		select {
		case <-c.done:
			return
		case pkt := <-c.sendCh:
			if err := writeMQTTFrame(c.conn, pkt); err != nil {
				c.close()
				return
			}
		}
	}
}

func (c *brokerClient) readLoop() {
	for {
		if c.keepAlive > 0 {
			// MQTT 3.1.1 allows up to 1.5x keepAlive before server disconnects idle client.
			_ = c.conn.SetReadDeadline(time.Now().Add(c.keepAlive*2 + 5*time.Second))
		} else {
			_ = c.conn.SetReadDeadline(time.Time{})
		}

		pktType, flags, body, err := readMQTTPacket(c.reader)
		if err != nil {
			return
		}

		switch pktType {
		case mqttPktPublish:
			if err := c.handlePublish(flags, body); err != nil {
				return
			}
		case mqttPktPuback:
			// QoS 1 acknowledgement from subscriber; no action needed.
		case mqttPktPubrec:
			if len(body) >= 2 {
				_ = c.enqueue([]byte{(mqttPktPubrel << 4) | 0x02, 0x02, body[0], body[1]})
			}
		case mqttPktPubrel:
			if len(body) < 2 {
				return
			}
			pid := binary.BigEndian.Uint16(body[:2])
			delete(c.inboundQoS2, pid)
			_ = c.enqueue([]byte{mqttPktPubcomp << 4, 0x02, body[0], body[1]})
		case mqttPktPubcomp:
			// QoS 2 completion from subscriber; no action needed.
		case mqttPktSubscribe:
			if (flags&0x0F) != 0x02 || c.handleSubscribe(body) != nil {
				return
			}
		case mqttPktUnsubscribe:
			if (flags&0x0F) != 0x02 || c.handleUnsubscribe(body) != nil {
				return
			}
		case mqttPktPingreq:
			_ = c.enqueue([]byte{mqttPktPingresp << 4, 0x00})
		case mqttPktDisconnect:
			c.graceful.Store(true)
			return
		default:
			return
		}
	}
}

func (c *brokerClient) handlePublish(flags byte, body []byte) error {
	qos := (flags >> 1) & 0x03
	if qos > 2 {
		return errors.New("invalid publish qos")
	}
	retain := (flags & 0x01) != 0

	pos := 0
	topic, n, err := readMQTTString(body[pos:])
	if err != nil || topic == "" || strings.ContainsAny(topic, "+#") {
		return errors.New("invalid publish topic")
	}
	pos += n

	var pid uint16
	if qos > 0 {
		if pos+2 > len(body) {
			return errors.New("missing packet identifier")
		}
		pid = binary.BigEndian.Uint16(body[pos : pos+2])
		pos += 2
	}

	payload := append([]byte(nil), body[pos:]...)

	switch qos {
	case 0:
		c.broker.publish(topic, payload, 0, retain)
	case 1:
		c.broker.publish(topic, payload, 1, retain)
		c.enqueue([]byte{mqttPktPuback << 4, 0x02, byte(pid >> 8), byte(pid)})
	case 2:
		if _, dup := c.inboundQoS2[pid]; !dup {
			c.inboundQoS2[pid] = struct{}{}
			c.broker.publish(topic, payload, 2, retain)
		}
		c.enqueue([]byte{mqttPktPubrec << 4, 0x02, byte(pid >> 8), byte(pid)})
	}
	return nil
}

func (c *brokerClient) handleSubscribe(body []byte) error {
	if len(body) < 3 {
		return errors.New("short subscribe packet")
	}
	pidHi, pidLo := body[0], body[1]
	pos := 2

	var added []brokerSubscription
	var returnCodes []byte
	for pos < len(body) {
		filter, n, err := readMQTTString(body[pos:])
		if err != nil || filter == "" {
			return errors.New("invalid subscribe filter")
		}
		pos += n
		if pos >= len(body) {
			return errors.New("missing subscribe qos")
		}
		reqQoS := body[pos]
		pos++
		if reqQoS > 2 {
			returnCodes = append(returnCodes, 0x80)
			continue
		}
		granted := reqQoS
		if granted > 1 {
			granted = 1
		}
		added = append(added, brokerSubscription{filter: filter, qos: granted})
		returnCodes = append(returnCodes, granted)
	}
	if len(returnCodes) == 0 {
		return errors.New("empty subscribe list")
	}

	c.subMu.Lock()
	for _, s := range added {
		c.subs[s.filter] = s.qos
	}
	c.subMu.Unlock()

	ackPayload := make([]byte, 2+len(returnCodes))
	ackPayload[0] = pidHi
	ackPayload[1] = pidLo
	copy(ackPayload[2:], returnCodes)
	c.enqueue(buildMQTTPacket(mqttPktSuback, 0, ackPayload))

	// Deliver matching retained messages after SUBACK so clients see current state (e.g. LWT).
	c.broker.mu.RLock()
	var matched []struct {
		msg retainedMessage
		qos byte
	}
	for _, msg := range c.broker.retained {
		bestQoS := byte(0)
		hit := false
		for _, s := range added {
			if mqttTopicMatch(s.filter, msg.topic) {
				if !hit || s.qos > bestQoS {
					bestQoS = s.qos
				}
				hit = true
			}
		}
		if hit {
			matched = append(matched, struct {
				msg retainedMessage
				qos byte
			}{msg: msg, qos: bestQoS})
		}
	}
	c.broker.mu.RUnlock()

	for _, item := range matched {
		var outPID uint16
		if item.qos > 0 {
			outPID = c.nextPacketID()
		}
		c.enqueue(buildPublishPacket(item.msg.topic, item.msg.payload, item.qos, true, outPID))
	}
	return nil
}

func (c *brokerClient) handleUnsubscribe(body []byte) error {
	if len(body) < 2 {
		return errors.New("short unsubscribe packet")
	}
	pidHi, pidLo := body[0], body[1]
	pos := 2
	var filters []string
	for pos < len(body) {
		filter, n, err := readMQTTString(body[pos:])
		if err != nil {
			return errors.New("invalid unsubscribe filter")
		}
		pos += n
		filters = append(filters, filter)
	}

	c.subMu.Lock()
	for _, f := range filters {
		delete(c.subs, f)
	}
	c.subMu.Unlock()

	c.enqueue([]byte{mqttPktUnsuback << 4, 0x02, pidHi, pidLo})
	return nil
}

func (c *brokerClient) matchingQoS(topic string) (byte, bool) {
	c.subMu.RLock()
	defer c.subMu.RUnlock()
	matched := false
	best := byte(0)
	for filter, qos := range c.subs {
		if mqttTopicMatch(filter, topic) {
			if !matched || qos > best {
				best = qos
			}
			matched = true
		}
	}
	return best, matched
}

func (c *brokerClient) nextPacketID() uint16 {
	for {
		v := uint16(c.nextPID.Add(1))
		if v != 0 {
			return v
		}
	}
}

func (b *EmbeddedBroker) publish(topic string, payload []byte, qos byte, retain bool) {
	type target struct {
		client *brokerClient
		qos    byte
	}

	b.mu.Lock()
	if retain {
		if len(payload) == 0 {
			delete(b.retained, topic)
		} else {
			b.retained[topic] = retainedMessage{
				topic:   topic,
				payload: append([]byte(nil), payload...),
			}
		}
	}
	clients := make([]*brokerClient, 0, len(b.clients))
	for c := range b.clients {
		clients = append(clients, c)
	}
	b.mu.Unlock()

	var targets []target
	for _, c := range clients {
		subQoS, ok := c.matchingQoS(topic)
		if !ok {
			continue
		}
		outQoS := qos
		if subQoS < outQoS {
			outQoS = subQoS
		}
		if outQoS > 1 {
			outQoS = 1
		}
		targets = append(targets, target{client: c, qos: outQoS})
	}

	// Fast path: reuse serialized QoS-0 frame across all QoS-0 subscribers.
	var qos0Frame []byte
	for _, t := range targets {
		if t.qos == 0 {
			if qos0Frame == nil {
				qos0Frame = buildPublishPacket(topic, payload, 0, false, 0)
			}
			t.client.enqueue(qos0Frame)
			continue
		}
		pid := t.client.nextPacketID()
		t.client.enqueue(buildPublishPacket(topic, payload, t.qos, false, pid))
	}
}

func mqttTopicMatch(filter, topic string) bool {
	if filter == "" || topic == "" {
		return false
	}
	// MQTT-4.7.2-1: wildcards at root must not match $-prefixed system topics.
	if strings.HasPrefix(topic, "$") && (strings.HasPrefix(filter, "+") || strings.HasPrefix(filter, "#")) {
		return false
	}

	fParts := strings.Split(filter, "/")
	tParts := strings.Split(topic, "/")

	for i := 0; i < len(fParts); i++ {
		f := fParts[i]
		if f == "#" {
			// Must be the final segment; matches zero or more remaining levels (including parent).
			return i == len(fParts)-1
		}
		if i >= len(tParts) {
			return false
		}
		if f == "+" || f == tParts[i] {
			continue
		}
		return false
	}
	return len(fParts) == len(tParts)
}

func readMQTTPacket(r *bufio.Reader) (byte, byte, []byte, error) {
	b0, err := r.ReadByte()
	if err != nil {
		return 0, 0, nil, err
	}
	pktType := b0 >> 4
	flags := b0 & 0x0F

	remLen, err := readRemainingLength(r)
	if err != nil {
		return 0, 0, nil, err
	}
	if remLen > maxMQTTPacketBytes {
		return 0, 0, nil, errors.New("mqtt packet exceeds max size")
	}

	body := make([]byte, remLen)
	if remLen > 0 {
		if _, err := io.ReadFull(r, body); err != nil {
			return 0, 0, nil, err
		}
	}
	return pktType, flags, body, nil
}

func readRemainingLength(r *bufio.Reader) (int, error) {
	multiplier := 1
	value := 0
	for i := 0; i < 4; i++ {
		encoded, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		value += int(encoded&0x7F) * multiplier
		if encoded&0x80 == 0 {
			return value, nil
		}
		multiplier *= 128
	}
	return 0, errors.New("malformed remaining length")
}

func readMQTTBytes(b []byte) ([]byte, int, error) {
	if len(b) < 2 {
		return nil, 0, io.ErrUnexpectedEOF
	}
	n := int(binary.BigEndian.Uint16(b[:2]))
	if len(b) < 2+n {
		return nil, 0, io.ErrUnexpectedEOF
	}
	return b[2 : 2+n], 2 + n, nil
}

func readMQTTString(b []byte) (string, int, error) {
	raw, consumed, err := readMQTTBytes(b)
	if err != nil {
		return "", 0, err
	}
	return string(raw), consumed, nil
}

func buildMQTTPacket(pktType, flags byte, payload []byte) []byte {
	var remLenBuf [4]byte
	n := encodeRemainingLength(remLenBuf[:], len(payload))
	out := make([]byte, 1+n+len(payload))
	out[0] = (pktType << 4) | (flags & 0x0F)
	copy(out[1:1+n], remLenBuf[:n])
	copy(out[1+n:], payload)
	return out
}

func buildPublishPacket(topic string, payload []byte, qos byte, retain bool, pid uint16) []byte {
	var flags byte
	flags |= (qos & 0x03) << 1
	if retain {
		flags |= 0x01
	}
	varLen := 2 + len(topic)
	if qos > 0 {
		varLen += 2
	}
	body := make([]byte, varLen+len(payload))
	binary.BigEndian.PutUint16(body[0:2], uint16(len(topic)))
	copy(body[2:2+len(topic)], topic)
	pos := 2 + len(topic)
	if qos > 0 {
		binary.BigEndian.PutUint16(body[pos:pos+2], pid)
		pos += 2
	}
	copy(body[pos:], payload)
	return buildMQTTPacket(mqttPktPublish, flags, body)
}

func encodeRemainingLength(dst []byte, x int) int {
	i := 0
	for {
		encoded := byte(x % 128)
		x /= 128
		if x > 0 {
			encoded |= 0x80
		}
		dst[i] = encoded
		i++
		if x == 0 {
			return i
		}
	}
}

func writeMQTTFrame(conn net.Conn, pkt []byte) error {
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	for len(pkt) > 0 {
		n, err := conn.Write(pkt)
		if err != nil {
			return err
		}
		pkt = pkt[n:]
	}
	return nil
}
