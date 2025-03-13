package jitter

import (
	"hash/fnv"
	"log"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/metric"
	"github.com/influxdata/telegraf/plugins/processors"
)

var sampleConfig = `
## Compute jitter for counter fields.
##
## This plugin compute the jitter based on the current and previous metric using a cache mechanism.
## List of fields for which the jitter must be computed
## 
fields = ["in_octets","out_octets"]
##
## Alert when Jitter is detected
jitter_max= 1s
## Interval rate
interval = 30s
##Period set the time to wait between two cache cleanup operation
period = "5m"
##Retention set how long the data are cached before being removed
##Each time an arriving metric matches an entry in the cache, the entry is updated. Though, only data that had no matches during this retention window are removed.
retention = "1h"
`

type Jitter struct {
	Log          telegraf.Logger
	Fields       []string `toml:"fields"`
	Interval     string   `toml:"interval"`
	Jitter_max   string   `toml:"jitter_max"`
	fields_map   map[string]struct{}
	initialized  bool
	Period       string `toml:"period"`
	Retention    string `toml:"retention"`
	last_cleared time.Time
	cache        map[uint64]compute
}

type compute struct {
	field_name  string
	field_value float64
	tm          time.Time
}

func (p *Jitter) SampleConfig() string {
	return sampleConfig
}

func (p *Jitter) Description() string {
	return "Compute the jitter"
}

func hash(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

func (p *Jitter) Apply(metrics ...telegraf.Metric) []telegraf.Metric {
	//var nb_deleted int
	//var t_period time.Duration
	//var t_retention time.Duration
	t_period, _ := time.ParseDuration(p.Period)
	t_retention, _ := time.ParseDuration(p.Retention)
	t_jitter_max, _ := time.ParseDuration(p.Jitter_max)
	t_interval, _ := time.ParseDuration(p.Interval)

	if !p.initialized {
		logPrintf("Initializing...")
		p.cache = make(map[uint64]compute)
		p.fields_map = make(map[string]struct{})
		for _, name := range p.Fields {
			p.fields_map[name] = struct{}{}
			logPrintf("Adding field %v", name)
		}
		p.initialized = true
		p.last_cleared = time.Now()
	}
	if time.Now().After(p.last_cleared.Add(t_period)) {
		logPrintf("Time to clean the cache, nb cache entries %v", len(p.cache))
		nb_deleted := 0
		for k, v := range p.cache {
			logPrintf("Hashid %v time %v", k, v.tm)
			if time.Now().After(v.tm.Add(t_retention)) {
				logPrintf("delete entry %v from cache", k)
				delete(p.cache, k)
				nb_deleted += 1
			}
		}
		logPrintf("%v entries deleted from cache", nb_deleted)
		p.last_cleared = time.Now()
	}

	alarmMetric := []telegraf.Metric{}

	for _, mymetric := range metrics {
		tags := ""
		for _, tag := range mymetric.TagList() {
			tags = tags + tag.Key + tag.Value
		}
		for _, field := range mymetric.FieldList() {
			// Check if the field belongs to the list of fields that need to be computed
			if _, ok := p.fields_map[field.Key]; ok {
				//check if the value of the field can be converted to float64
				if value, ok := convert(field.Value); ok {
					a := compute{
						field_name:  field.Key,
						field_value: value,
						tm:          mymetric.Time(),
					}
					// build a unique id based on the field name and the belonging tags
					id := hash(field.Key + tags)
					// check if an entry exists for this ID in the cache
					if _, ok := p.cache[id]; ok {
						delta := mymetric.Time().Sub(p.cache[id].tm).Seconds()
						if delta >= float64(t_interval.Seconds()+t_jitter_max.Seconds()) || delta <= float64(t_interval.Seconds()-t_jitter_max.Seconds()) {
							newAlarm := metric.New("JITTER_MEASUREMENT", map[string]string{}, map[string]interface{}{"exception": delta}, mymetric.Time())
							for k, v := range mymetric.Tags() {
								newAlarm.AddTag(k, v)
							}
							alarmMetric = append(alarmMetric, newAlarm)
							logPrintf("One metric exeeded the max jitter%v", id)
						}
						p.cache[id] = a
					} else {
						logPrintf("Creating cache entry for metric with hashid %v", id)
						p.cache[id] = a
					}
				} else {
					logPrintf("Value cannot be converted to float %v", field.Value)
				}
			}
		}
	}
	return append(metrics, alarmMetric...)
}

func logPrintf(format string, v ...interface{}) {
	log.Printf("D! [processors.jitter] "+format, v...)
}

func convert(in interface{}) (float64, bool) {
	switch v := in.(type) {
	case float64:
		return v, true
	case int64:
		return float64(v), true
	case uint64:
		return float64(v), true
	default:
		return 0, false
	}
}

func init() {
	processors.Add("jitter", func() telegraf.Processor {
		return &Jitter{}
	})
}
