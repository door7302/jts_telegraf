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

const maxTagStackDepth = 5
const layout = "2006-01-02 15:04:05 MST"

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
	fields      map[string]fieldEntry
}

type fieldEntry struct {
	shortName string
	fieldType string
	tags      []string
}

type tagEntry struct {
	shortName    string
	currentValue string
	visited      bool
}

type netconfMetric struct {
	shortName    string
	fieldType    string
	currentValue interface{}
	visited      bool
	tags         []string
}

// Start the ssh listener service
func (c *NETCONF) Start(acc telegraf.Accumulator) error {
	var ctx context.Context

	tags := make(map[string]tagEntry)
	requests := make([]req, 0)
	parents := make(map[string]map[string][]string)

	c.acc = acc
	ctx, c.cancel = context.WithCancel(context.Background())

	// Validate configuration
	if time.Duration(c.Redial).Nanoseconds() <= 0 {
		return fmt.Errorf("redial duration must be positive")
	}

	// parse the configuration to create the requests
	for _, s := range c.Subscriptions {
		var r req

		r.measurement = s.Name
		r.rpc = s.Rpc
		r.interval = uint64(time.Duration(s.SampleInterval).Nanoseconds())
		r.fields = make(map[string]fieldEntry)
		parents[s.Rpc] = map[string][]string{}

		// first parse paths
		for _, p := range s.Fields {
			var field fieldEntry
			field.tags = make([]string, 0)

			split_field := strings.Split(p, ":")
			if len(split_field) != 2 {
				c.Log.Errorf("Malformed field - skip it: %p", p)
				continue
			}
			split_xpath := strings.Split(split_field[0], "/")

			xpath := ""
			shortName := ""
			parent := ""

			for _, e := range split_xpath {
				// there is an attribute
				if strings.Contains(e, "[") && strings.Contains(e, "]") {
					// extract the key and concatenate with xpath
					node := e[0:strings.Index(e, "[")]
					attribut := e[strings.Index(e, "[")+1 : strings.Index(e, "]")]

					// update xpath and parent
					parent = xpath + node
					xpath += node + "/"

					field.tags = append(field.tags, xpath+attribut)

					// Save tag
					tags[xpath+attribut] = tagEntry{shortName: attribut}

					// save child of the parent if new
					_, ok := parents[s.Rpc][parent]
					if !ok {
						parents[s.Rpc][parent] = make([]string, 0)
					}
					exist := false
					for _, e := range parents[s.Rpc][parent] {
						if e == xpath+attribut {
							exist = true
							break
						}
					}
					if !exist {
						parents[s.Rpc][parent] = append(parents[s.Rpc][parent], xpath+attribut)
					}

				} else {
					xpath += e + "/"
					shortName = e
				}
			}
			// Remove trailing /
			xpath = xpath[:len(xpath)-1]
			field.shortName = shortName
			field.fieldType = split_field[1]

			// save child of the parent if new
			exist := false
			for _, e := range parents[s.Rpc][parent] {
				if e == xpath {
					exist = true
					break
				}
			}
			if !exist {
				parents[s.Rpc][parent] = append(parents[s.Rpc][parent], xpath)
			}

			// Update fields map
			r.fields[xpath] = field
		}

		requests = append(requests, r)
	}

	// Create a goroutine for each device, dial and subscribe
	c.wg.Add(len(c.Addresses))
	for _, addr := range c.Addresses {
		go func(address string) {
			defer c.wg.Done()
			for ctx.Err() == nil {
				if err := c.subscribeNETCONF(ctx, address, c.Username, c.Password, requests, tags, parents); err != nil && ctx.Err() == nil {
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
func (c *NETCONF) subscribeNETCONF(ctx context.Context, address string, u string, p string, r []req, allTags map[string]tagEntry, allParents map[string]map[string][]string) error {

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

	metricToSend := make(map[string]map[string]netconfMetric)
	tagTable := make(map[string]map[string]tagEntry)

	// prepare the map for searching metrics - unique per router - derived from initial request
	// for each RPC and each field
	for _, req := range r {
		metricToSend[req.rpc] = make(map[string]netconfMetric)
		tagTable[req.rpc] = make(map[string]tagEntry)
		for k, v := range req.fields {
			metricToSend[req.rpc][k] = netconfMetric{shortName: v.shortName, fieldType: v.fieldType, currentValue: "", visited: false, tags: v.tags}
		}
		for k, v := range allTags {
			tagTable[req.rpc][k] = v
		}
	}
	// compute tick - add jitter to avoid thread sync
	jitter := time.Duration(1000 + rand.Intn(10))
	tick := jitter * time.Millisecond

	// First find out the min interval btw all RPC
	min := uint64(100000)
	for _, v := range r {
		min = minUint64(min, v.interval)
	}
	// Init counter per RPC - distribute evently the RPC over the min time frame
	taskInterval := uint64(time.Duration((float64(min) / float64(len(r))) * float64(time.Second)))
	counters := make(map[string]uint64)
	for i, v := range r {
		counters[v.rpc] = uint64(i) * taskInterval
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
							// First check if xpath is a parent - if parent you need to prepare metric to send
							pval, ok := allParents[req.rpc][s]
							if ok {
								// time to check all fields attached to the parent
								for _, f := range pval {
									// first check field has been visited or not
									med, ok := metricToSend[req.rpc][f]
									if ok && med.visited {
										// create the metric
										medTags := map[string]string{
											"device": address,
										}
										for _, z := range med.tags {
											// check if tag has been visited before adding it
											tVal, ok := tagTable[req.rpc][z]
											if ok {
												if tVal.visited {
													medTags[tVal.shortName] = tVal.currentValue
												}
											}
										}
										// add metric to groupper
										if err := grouper.Add(req.measurement, medTags, timestamp, med.shortName, med.currentValue); err != nil {
											c.Log.Errorf("cannot add to grouper: %v", err)
										}
									}
								}
								// now reset all fields and tags associated to parent
								for _, f := range pval {
									med, ok := metricToSend[req.rpc][f]
									// this is a field
									if ok {
										med.currentValue = ""
										med.visited = false
										metricToSend[req.rpc][f] = med
									} else {
										// this is a tag
										tag, ok := tagTable[req.rpc][f]
										if ok {
											tag.currentValue = ""
											tag.visited = false
											tagTable[req.rpc][f] = tag
										}
									}
								}
							} else {

								// if not parent check if it's a tag
								tval, ok := tagTable[req.rpc][s]
								if ok {
									tval.currentValue = value
									tval.visited = true
									tagTable[req.rpc][s] = tval

								} else {
									// otherwise check if it's a field to track
									fval, ok := metricToSend[req.rpc][s]
									if ok {
										switch fval.fieldType {
										case "int":
											fval.currentValue, err = strconv.Atoi(value)
											if err != nil {
												// keep string as type in case of error
												fval.currentValue = value
											}
											fval.visited = true
										case "float":
											fval.currentValue, err = strconv.ParseFloat(value, 64)
											if err != nil {
												// keep string as type in case of error
												fval.currentValue = value
											}
											fval.visited = true
										case "epoch":
											t, err := time.Parse(layout, value)
											if err != nil {
												// keep string as type in case of error
												fval.currentValue = value
											} else {
												fval.currentValue = t.UnixNano()
											}
											fval.visited = true
										default:
											// Keep value as string for all other types
											fval.currentValue = value
											fval.visited = true
										}
										metricToSend[req.rpc][s] = fval
									}
								}
							}

							// remove the last elem of the xpath list
							if len(xpath) > 0 {
								xpath = xpath[:len(xpath)-1]
							}

						case xml.CharData:
							// extract value
							value = strings.TrimSpace(strings.ReplaceAll(string(element), "\n", ""))
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
  addresses = ["10.49.234.1"]

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
    ## Each entry in the list is made of: <xpath>:<type>
    ## - xpath lite 
    ## - a type of encoding (supported types : int, float, string, epoch)
    ## 
    ## The xpath lite should follow the rpc reply XML document. Optional: you can include btw [] the KEY's name that must use to detect the loop 
    fields = ["/interface-information/physical-interface[name]/speed:string", 
            "/interface-information/physical-interface[name]/traffic-statistics/input-packets:int",
            "/interface-information/physical-interface[name]/traffic-statistics/output-packets:int",
            ]
    ## Interval to request the RPC
    sample_interval = "30s"

  ## Another example with 2 levels of key
  [[inputs.netconf_junos.subscription]]
    name = "COS"
    junos_rpc = "<get-interface-queue-information></get-interface-queue-information>"
    fields = ["/interface-information/physical-interface[name]/queue-counters/queue[queue-number]/queue-counters-queued-packets:int",]
    sample_interval = "60s"
`

// simple unint64 min func
func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

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
