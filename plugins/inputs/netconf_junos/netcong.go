package netconf_junos

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/config"
	"github.com/influxdata/telegraf/metric"
	"github.com/influxdata/telegraf/plugins/inputs"
	"github.com/openshift-telco/go-netconf-client/netconf"
	"github.com/openshift-telco/go-netconf-client/netconf/message"
	"golang.org/x/crypto/ssh"
)

// Netconf plugin instance
type NETCONF struct {
	Addresses     []string       `toml:"addresses"`
	Subscriptions []Subscription `toml:"subscription"`

	// Netconf target credentials
	Username string `toml:"username"`
	Password string `toml:"password"`

	// Redial
	Redial config.Duration `toml:"redial"`

	// Internal state
	acc    telegraf.Accumulator
	cancel context.CancelFunc
	wg     sync.WaitGroup

	Log telegraf.Logger
}

// Subscription for a Netconf client
type Subscription struct {
	Name   string   `toml:"name"`
	Rpc    string   `toml:"junos_rpc"`
	Fields []string `toml:"fields"`

	// Subscription mode and interval
	SampleInterval config.Duration `toml:"sample_interval"`
}

type req struct {
	measurement string
	interval    uint64
	rpc         string
	fieldList   []fieldEntry
	hashTable   map[string]xpathEntry
}

type fieldEntry struct {
	fieldName string
	tagLength int
}

type xpathEntry struct {
	shortName  string
	masterKeys []string
	metricType string
}

type netMetric struct {
	tagLength  int
	keyTag     []string
	valueTag   []string
	keyField   string
	valueField interface{}
	send       int
}

// Start the ssh listener service
func (c *NETCONF) Start(acc telegraf.Accumulator) error {
	var ctx context.Context
	var requests []req

	c.acc = acc
	ctx, c.cancel = context.WithCancel(context.Background())

	// Validate configuration
	if time.Duration(c.Redial).Nanoseconds() <= 0 {
		return fmt.Errorf("redial duration must be positive")
	}

	// parse the configuration to create the requests
	requests = make([]req, 0)
	for _, s := range c.Subscriptions {
		var r req
		r.measurement = s.Name
		r.rpc = s.Rpc
		r.interval = uint64(time.Duration(s.SampleInterval).Nanoseconds())
		r.hashTable = make(map[string]xpathEntry)
		r.fieldList = make([]fieldEntry, 0)

		// first parse paths
		for _, p := range s.Fields {
			split_field := strings.Split(p, ":")
			if len(split_field) != 2 {
				c.Log.Errorf("Malformed field - skip it: %p", p)
				continue
			}
			split_xpath := strings.Split(split_field[0], "/")
			xpath := ""
			last := ""
			numberOfTags := 0
			for _, e := range split_xpath {
				// there is an attribute
				if strings.Contains(e, "[") && strings.Contains(e, "]") {
					numberOfTags += 1
					// extract the key and concatenate with xpath
					text := e[0:strings.Index(e, "[")]
					attribut := e[strings.Index(e, "[")+1 : strings.Index(e, "]")]
					xpath += text + "/"
					// create the hashtable for fast search
					mapInstance, ok := r.hashTable[xpath+attribut]
					if !ok {
						r.hashTable[xpath+attribut] = xpathEntry{masterKeys: make([]string, 0), metricType: "tag", shortName: attribut}
						mapInstance = r.hashTable[xpath+attribut]
						mapInstance.masterKeys = append(mapInstance.masterKeys, p)
						r.hashTable[xpath+attribut] = mapInstance
					} else {
						mapInstance.masterKeys = append(mapInstance.masterKeys, p)
						r.hashTable[xpath+attribut] = mapInstance
					}
				} else {
					xpath += e + "/"
					last = e
				}
			}
			mapInstance, ok := r.hashTable[xpath[0:len(xpath)-1]]
			if !ok {
				r.hashTable[xpath[0:len(xpath)-1]] = xpathEntry{masterKeys: make([]string, 0), metricType: split_field[1], shortName: last}
				mapInstance = r.hashTable[xpath[0:len(xpath)-1]]
				mapInstance.masterKeys = append(mapInstance.masterKeys, p)
				r.hashTable[xpath[0:len(xpath)-1]] = mapInstance
			} else {
				mapInstance.masterKeys = append(mapInstance.masterKeys, p)
				r.hashTable[xpath[0:len(xpath)-1]] = mapInstance
			}
			r.fieldList = append(r.fieldList, fieldEntry{fieldName: p, tagLength: numberOfTags})
		}
		requests = append(requests, r)
	}

	// Create a goroutine for each device, dial and subscribe
	c.wg.Add(len(c.Addresses))
	for _, addr := range c.Addresses {
		go func(address string) {
			defer c.wg.Done()
			for ctx.Err() == nil {
				if err := c.subscribeNETCONF(ctx, address, c.Username, c.Password, requests); err != nil && ctx.Err() == nil {
					acc.AddError(err)
				}
				select {
				case <-ctx.Done():
				case <-time.After(time.Duration(c.Redial)):
				}
			}
		}(addr)
	}
	return nil
}

// subscribeNETCONF and extract telemetry data
func (c *NETCONF) subscribeNETCONF(ctx context.Context, address string, u string, p string, r []req) error {
	sshConfig := &ssh.ClientConfig{
		User:            u,
		Auth:            []ssh.AuthMethod{ssh.Password(p)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	// Open SSH Session
	session, err := netconf.DialSSH(fmt.Sprintf("%s:%d", address, 830), sshConfig)
	if err != nil {
		return fmt.Errorf("unable to open Netconf session for address %s: %v", address, err)
	}
	defer session.Close()

	// Exchange capa... Just send HELLO RPC
	capabilities := netconf.DefaultCapabilities
	err = session.SendHello(&message.Hello{Capabilities: capabilities})
	if err != nil {
		return fmt.Errorf("error while sending Hello for router %s: %v", address, err)
	}
	c.Log.Debugf("Connection to Netconf device %s established", address)
	defer c.Log.Debugf("Connection to Netconf device %s closed", address)

	// prepare the map for searching metrics - unique per router - derived from initial request
	var metricToSend map[string]map[string]netMetric
	metricToSend = make(map[string]map[string]netMetric)
	for _, req := range r {
		metricToSend[req.rpc] = make(map[string]netMetric)
		for _, k := range req.fieldList {
			metricToSend[req.rpc][k.fieldName] = netMetric{tagLength: k.tagLength, keyTag: make([]string, 0), valueTag: make([]string, 0), keyField: "", valueField: "", send: 0}
		}
	}

	// compute tick - add jitter to avoid thread sync
	jitter := time.Duration(1000 + rand.Intn(10))
	tick := jitter * time.Millisecond

	// Init counter per RPC
	counters := make(map[string]uint64)
	for _, v := range r {
		counters[v.rpc] = 0
	}

	// Loop until end
	for ctx.Err() == nil {
		start := time.Now().UnixNano()
		for _, req := range r {
			// check if it's time to issue RPC
			if counters[req.rpc] >= req.interval {
				timestamp := time.Now()
				rpc_start := timestamp.UnixNano()
				// Init metric containers
				grouper := metric.NewSeriesGrouper()

				// Reset counter for this RPC
				counters[req.rpc] = 0

				// Send RPC to router
				c.Log.Debugf("time to to issue the rpc %s for device %s", req.rpc, address)
				rpc := message.NewRPC(req.rpc)
				reply, err := session.SyncRPC(rpc, int32(60))
				if err != nil || reply == nil || strings.Contains(reply.Data, "<rpc-error>") {
					c.Log.Debugf("RPC error to Netconf device %s , rpc: %s", address, req.rpc)
					continue
				} else {
					c.Log.Debugf("rpc-reply received for rpc %s and device %s", req.rpc, address)

					// Made a buffer based on reply
					buffer := bytes.NewBuffer([]byte(reply.Data))
					decoder := xml.NewDecoder(buffer)

					// Now traverse XML tree and rebuild XPATH and fill expected metric
					xpath := make([]string, 0)
					value := ""
					last_tag := ""
					for {
						token, err := decoder.Token()
						if err != nil {
							// EOF
							break
						}
						switch element := token.(type) {
						case xml.StartElement:
							// append node to xpath
							xpath = append(xpath, element.Name.Local)
						case xml.EndElement:
							// rebuild the complete xpath
							s := "/"
							for _, x := range xpath {
								s += x + "/"
							}

							// Remove trailing /
							s = s[:len(s)-1]

							// remove the last elem of the xpath list
							if len(xpath) > 0 {
								xpath = xpath[:len(xpath)-1]
							}
							// check if xpath matches one field's xpath
							data, ok := req.hashTable[s]
							if ok {
								// Update TAG of all related metrics
								if data.metricType == "tag" {
									// if tag changes - it means its time to flush metric - we have looped to a new entry
									if last_tag != "" && last_tag != value {
										for _, k := range data.masterKeys {
											v, ok := metricToSend[req.rpc][k]
											if ok {
												// Time to add the metrics to the grouper
												if v.send == 2 {
													// reinit the metric
													tags := map[string]string{
														"device": address,
													}
													for ind := 0; ind < v.tagLength; ind++ {
														tags[v.keyTag[ind]] = v.valueTag[ind]
													}

													if err := grouper.Add(req.measurement, tags, timestamp, v.keyField, v.valueField); err != nil {
														c.Log.Errorf("cannot add to grouper: %v", err)
													}
												}
												v.send = 0
												v.keyTag = make([]string, 0)
												v.valueTag = make([]string, 0)
												metricToSend[req.rpc][k] = v
											}
										}
									}
									last_tag = value
									for _, k := range data.masterKeys {
										v, ok := metricToSend[req.rpc][k]
										if ok {

											// update TAG for each metric
											v.keyTag = append(v.keyTag, data.shortName)
											v.valueTag = append(v.valueTag, value)
											// only add token to send variable only if we reach the number of expected tag

											if len(v.valueTag) == v.tagLength {
												v.send += 1
											}
											metricToSend[req.rpc][k] = v
										}
									}

								} else {
									// Update field of all related metrics
									for _, k := range data.masterKeys {
										v, ok := metricToSend[req.rpc][k]
										if ok {

											// update TAG for each metric
											v.keyField = data.shortName
											switch data.metricType {
											case "int":
												v.valueField, err = strconv.Atoi(value)
												if err != nil {
													// keep string as type in case of error
													v.valueField = value
												}
											case "float":
												v.valueField, err = strconv.ParseFloat(value, 64)
												if err != nil {
													// keep string as type in case of error
													v.valueField = value
												}
											default:
												// Keep value as string for all other types
												v.valueField = value
											}
											v.send += 1

											// Take into account metric without tag - no loop
											if !strings.Contains(k, "[") {
												if v.send == 1 {
													// reinit the metric
													tags := map[string]string{
														"device": address,
													}
													if err := grouper.Add(req.measurement, tags, timestamp, v.keyField, v.valueField); err != nil {
														c.Log.Errorf("cannot add to grouper: %v", err)
													}
													v.send = 0
												}
											}
											metricToSend[req.rpc][k] = v
										}
									}
								}
							}
						case xml.CharData:
							// extract value
							value = strings.ReplaceAll(string(element), "\n", "")
						}

					}
					// Add grouped measurements
					for _, metricToAdd := range grouper.Metrics() {
						c.acc.AddMetric(metricToAdd)
					}
					delta_rpc := time.Now().UnixNano() - rpc_start
					c.Log.Debugf("rpc handling for rpc %s and device %s toke %s", req.rpc, address, time.Duration(uint64(delta_rpc)).String())
				}
			}
		}
		delta := time.Now().UnixNano() - start
		if uint64(delta) < uint64(tick) {
			time.Sleep(tick)
		}
		delta = time.Now().UnixNano() - start
		// update counters
		for k, _ := range counters {
			counters[k] += uint64(delta)
		}
	}
	return nil
}

// Stop listener and cleanup
func (c *NETCONF) Stop() {
	c.cancel()
	c.wg.Wait()
}

const sampleConfig = `
[[inputs.netconf_junos]]
  ## Address of the Juniper NETCONF server
  addresses = ["10.49.234.114"]

  ## define credentials
  username = "lab"
  password = "lab123"

  ## redial in case of failures after
  redial = "10s"

  [[inputs.netconf_junos.subscription]]
    ## Name of the measurement that will be emitted
    name = "ifcounters"

    ## the JUNOS RPC to collect 
    junos_rpc = "<get-interface-information><statistics/></get-interface-information>"
  
    ## A list of xpath lite + type to collect / encode 
    ## Each entry in the list is made of:
    ## - xpath lite 
    ## - a type of encoding (supported types : int, float, string)
    ## 
    ## The xpath lite should follow the rpc reply XML document. Optional: you can include btw [] the KEY's name that must use to detect the loop 
    ## Only one loop field must be used and should be the same for all fields part of the same RPC 
    fields = ["/interface-information/physical-interface[ifname]/speed:string", 
            "/interface-information/physical-interface[ifname]/traffic-statistics/input-packets:int",
            "/interface-information/physical-interface[ifname]/traffic-statistics/output-packets:int",
            ]

    ## Interval to request the RPC
    sample_interval = "10s"
`

// SampleConfig of plugin
func (c *NETCONF) SampleConfig() string {
	return sampleConfig
}

// Description of plugin
func (c *NETCONF) Description() string {
	return "Netconf Junos input plugin"
}

// Gather plugin measurements (unused)
func (c *NETCONF) Gather(_ telegraf.Accumulator) error {
	return nil
}
func New() telegraf.Input {
	return &NETCONF{
		Redial: config.Duration(10 * time.Second),
	}
}
func init() {
	inputs.Add("netconf_junos", New)
}
