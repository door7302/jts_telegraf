package rate

import (
	"log"
	"time"
	"hash/fnv"
    "github.com/influxdata/telegraf"
    "github.com/influxdata/telegraf/plugins/processors"
)

var sampleConfig = `
## Compute rate for counter fields.
##
## This plugin compute the rate based on the current and previous metric using a cache mechanism.
## The computed rate is appended to the metric as a new field leaving the source fields untouched.
## List of fields for which the rate must be computed
## 
fields = ["in_octets","out_octets"]
##
## Base rate is /s, factor can be used to adjust (bytes to bits factor = 8 or seconds to minutes factor = 60)
factor =  8
## Workaround for MCP11 bug that emit multiple unrefreshed counters in a short period of time, plugin not compute rate if the elapsed time between the cache data and the current data is less than this value (safe to be set to 10s).
delta_min = 10s
## Suffix set characters to be appended to the original's field name
suffix ="_rate"
##
##Period set the time to wait between two cache cleanup operation
period = "5m"
##Retention set how long the data are cached before being removed
##Each time an arriving metric matches an entry in the cache, the entry is updated. Though, only data that had no matches during this retention window are removed.
retention = "1h"
`

type Rate struct {
	Log   		telegraf.Logger
	Fields		[]string	`toml:"fields"`
	Suffix		string		`toml:"suffix"`
	Factor		float64		`toml:"factor"`
	Delta_min   string		`toml:"delta_min"`
	fields_map	map[string]struct{}
	initialized bool
	Period		string		`toml:"period"`
	Retention 	string		`toml:"retention"`
	last_cleared	time.Time
	cache       map[uint64]compute
	}

type compute struct {
	field_name string
	field_value   float64
	tm time.Time
}

func(p * Rate) SampleConfig() string {
    return sampleConfig
}

func(p * Rate) Description() string {
    return "Compute the rate"
}

func hash(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

func(p * Rate) Apply(metrics...telegraf.Metric)[] telegraf.Metric {
	//var nb_deleted int
	//var t_period time.Duration
	//var t_retention time.Duration
	t_period,_ := time.ParseDuration(p.Period)
	t_retention,_ := time.ParseDuration(p.Retention)
	t_delta_min,_ := time.ParseDuration(p.Delta_min)
	if !p.initialized {
		logPrintf("Initializing...")
		p.cache = make(map[uint64]compute)
		p.fields_map = make(map[string]struct{})
		for _,name := range p.Fields{
			p.fields_map[name] = struct{}{}
			logPrintf("Adding field %v", name)
		}
		p.initialized = true
		p.last_cleared = time.Now()
	}
	if time.Now().After(p.last_cleared.Add(t_period)) {
		logPrintf("Time to clean the cache, nb cache entries %v",len(p.cache))
		nb_deleted := 0
		for k,v := range p.cache {
			logPrintf("Hashid %v time %v",k,v.tm)
			if time.Now().After(v.tm.Add(t_retention)) {
				logPrintf("delete entry %v from cache",k)
				delete(p.cache,k)
				nb_deleted +=1
			}
		}
		logPrintf("%v entries deleted from cache",nb_deleted)
		p.last_cleared = time.Now()
	}
	for _, metric := range metrics {
		tags := ""
		for _, tag := range metric.TagList() {
			tags = tags + tag.Key + tag.Value
		}
		for _, field := range metric.FieldList() {
			// Check if the field belongs to the list of fields that need to be computed
			if _, ok := p.fields_map[field.Key]; ok{
				//check if the value of the field can be converted to float64
				if value, ok := convert(field.Value); ok {
					a := compute{
						field_name: field.Key,
						field_value: value,
						tm:	metric.Time(),
					}
					// build a unique id based on the field name and the belonging tags
					id := hash(field.Key+tags)
					// check if an entry exists for this ID in the cache
					if _, ok := p.cache[id]; ok {
						delta := metric.Time().Sub(p.cache[id].tm).Seconds()
						if delta > float64(t_delta_min.Seconds()) {
							field_rate := (value - p.cache[id].field_value)*p.Factor / float64(delta)
							if field_rate >= 0 {
								logPrintf("Adding field %v for metric with hashid %v",field.Key+p.Suffix, id)
								// The result is then added as a new field to the metric
								metric.AddField(field.Key+p.Suffix,field_rate)
								// The cache is updated with the latest value
								logPrintf("Updating cache entry for metric with hashid %v", id)
								p.cache[id] = a									
							} else {
								logPrintf("Negative rate discarded, reset counter has occured on hashid %v", id)
								logPrintf("Updating cache entry for metric with hashid %v", id)
								p.cache[id] = a		
							}
						} else {
							logPrintf("Skip cause delta_min constraint not met for metric with hashid %v", id)
						}
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
	return metrics
}

func logPrintf(format string, v...interface {}) {
    log.Printf("D! [processors.rate] " + format, v...)
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
    processors.Add("rate", func() telegraf.Processor {
        return &Rate {}
    })
}
