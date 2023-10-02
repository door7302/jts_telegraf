package Monitoring

import (
	"log"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/metric"
    "github.com/influxdata/telegraf/plugins/processors"
)

var sampleConfig = `
## Monitoring plugin monitors some fields' value and generates some specific metrics
## Monitoring's metrics are sent to the "measurement" name 
## Monitoring's metrics contain a specific tag with a key = "tag_name"
## Monitoring plugin uses a cache to compute delta or delta_rate 
## "Period" set the time to wait between two cache cleanup operation
## "Retention" set how long the data are cached before being removed
## Each time an arriving metric matches an entry in the cache, the entry is updated. 
## Though, only data that had no matches during this retention window are removed.
[[processors.monitoring]]
  order = 7
  measurement = "ALARMING"
  tag_name = "ALARM_TYPE"
  period = "10m"
  retention = "1h"
  
  ## For each monitoring probe we provide :
  ## The "alarm_name" of the alarm. It is actually the value of tag_name specified before 
  ## The "field" to monitor (int64, uint64 and float64 fields are supported)
  ## The "probe_type" = ["current"|"delta"|"delta_rate"] 
  ##   "current"      : we compare the current value of the field with the threshold 
  ##   "delta"        : we compare the diff/delta of the field with the threshold
  ##   "delta_rate"   : we compare the rate of the field with the threshold
  ##   "delta_percent"   : we compare the diff/delta in percentage of the field with the threshold
  ##   "min_value"       : Trigger alarm only if current value is greater than min_value 
  ## The "threshold field is a float field that defines the threshold of the probe
  ## The "operator" = ["lt", "gt", "eq"]. How we compare the value and the threshold (lower than, greater than, equal)
  ## The "copy_tag" option specifies if we need to copy some tags from the original's metric to the Monitoring's metric 
  ## If copy_tag is set we check "tags" list. If empty, all tags are copied, else only specified tags are copied into the Monitoring's metric
  ## 
  ## 
  ## The Monitoring metric has a single field named "exception" with conveys either the current value, the delta value or the rate value that triggered the Monitoring
  ## 
  [[processors.monitoring.probe]]
    alarm_name = "CPU_HIGH"
    field = "idle_cpu"
    probe_type = "delta_percent"
	threshold = 10.0
    min_nterval = 1000000.0
    operator = "gt"
    copy_tag = true
	tags = ["device","component_name"]


`

type Monitoring struct {
	Log   		telegraf.Logger
	Measurement	string	`toml:"measurement"`
	TagName		string		`toml:"tag_name"`
	Period		string		`toml:"period"`
	Retention 	string		`toml:"retention"`

	Probe []Probe    `toml:"probe"`
	fields_map	map[string]Probe
	initialized bool
	last_cleared	time.Time
	cache       map[uint64]compute
	}

	// Subscription for a GNMI client
type Probe struct {
	AlarmName string `toml:"alarm_name"`
	Field   string `toml:"field"`
	ProbeType string `toml:"probe_type"`
	Threshold float64 `toml:"threshold"`
	MinValue float64 `toml:"min_value"`
	Operator string `toml:"operator"`
	CopyTag bool `toml:"copy_tag"`
	Tags []string `toml:"tags"`
}

type compute struct {
	fields map[string]float64
	name   string
	tags   map[string]string
	tm time.Time
}

func(p * Monitoring) SampleConfig() string {
    return sampleConfig
}

func(p * Monitoring) Description() string {
    return "Monitor some KPI"
}

func(p * Monitoring) Apply(metrics...telegraf.Metric) []telegraf.Metric {
	//var nb_deleted int
	//var t_period time.Duration
	//var t_retention time.Duration
	t_period,_ := time.ParseDuration(p.Period)
	t_retention,_ := time.ParseDuration(p.Retention)
	if !p.initialized {
		logPrintf("Initializing...")
		p.cache = make(map[uint64]compute)
		p.fields_map = make(map[string]Probe)
		for _, monitor := range p.Probe{
			p.fields_map[monitor.Field] = monitor
			logPrintf("Adding field %v", monitor.Field)
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
	alarmMetric := []telegraf.Metric{}

	for _, mymetric := range metrics {
		hasField := false
		id := mymetric.HashID()
		a := compute{
			name:   mymetric.Name(),
			tags:   mymetric.Tags(),
			tm:		mymetric.Time(),
			fields:	make(map[string]float64),
		}
		for _, field := range mymetric.FieldList() {
			if _, ok := p.fields_map[field.Key]; ok{
				if a.fields[field.Key], ok = convert(field.Value); ok {
					hasField = true
				}
			}
		}
		if hasField {
			for key, value := range a.fields {
				if value >= p.fields_map[key].MinValue {
					thresholdReached := false
					switch p.fields_map[key].ProbeType {
					case "current":
						logPrintf("Mode Current")
						switch p.fields_map[key].Operator {
						case "lt":
							if value < p.fields_map[key].Threshold {
								logPrintf("Threshold reached for field %s. %f < %f",key,value,p.fields_map[key].Threshold)
								thresholdReached = true 
							}
						case "gt":
							if value > p.fields_map[key].Threshold {
								logPrintf("Threshold reached for field %s. %f > %f",key,value,p.fields_map[key].Threshold)
								thresholdReached = true 
							}
						case "eq":
							if value == p.fields_map[key].Threshold {
								logPrintf("Threshold reached for field %s. %f == %f",key,value,p.fields_map[key].Threshold)
								thresholdReached = true 
							}
						}
						if thresholdReached {
							newAlarm := metric.New(p.Measurement, map[string]string{}, map[string]interface{}{"exception": value},mymetric.Time())
							newAlarm.AddTag(p.TagName,p.fields_map[key].AlarmName)
							

							if p.fields_map[key].CopyTag {
								logPrintf("Copy Tags from original metric into monitoring metric")
								if len(p.fields_map[key].Tags) > 0 {
									logPrintf("Tags list is not empty - filetring tags")
									for _,v := range p.fields_map[key].Tags {
										if _, ok := a.tags[v]; ok{
											logPrintf("Copy Tags %s with value %s",v,a.tags[v])
											newAlarm.AddTag(v,a.tags[v])
										}
									}
								} else {
									logPrintf("Tags list is empty - copy all tags")
									for k,v := range a.tags {
										logPrintf("Copy Tags %s with value %s",k,v)
										newAlarm.AddTag(k,v)
									}

								}
							}
							alarmMetric = append(alarmMetric, newAlarm)
						}
					case "delta":
						logPrintf("Mode Delta")
						if _, ok := p.cache[id]; !ok  {
							logPrintf("Creating cache entry for metric with hashid %v", id)
							p.cache[id] = a
						// If cached data are available then the rate is computed
						} else  {
							if lv, ok := p.cache[id].fields[key]; ok {
								field_delta := value - lv
								switch p.fields_map[key].Operator {
								case "lt":
									if field_delta < p.fields_map[key].Threshold {
										logPrintf("Threshold reached for field %s. %f < %f",key,field_delta,p.fields_map[key].Threshold)
										thresholdReached = true 
									}
								case "gt":
									if field_delta > p.fields_map[key].Threshold {
										logPrintf("Threshold reached for field %s. %f > %f",key,field_delta,p.fields_map[key].Threshold)
										thresholdReached = true 
									}
								case "eq":
									if field_delta == p.fields_map[key].Threshold {
										logPrintf("Threshold reached for field %s. %f == %f",key,field_delta,p.fields_map[key].Threshold)
										thresholdReached = true 
									}
								}
								if thresholdReached {
									newAlarm := metric.New(p.Measurement, map[string]string{}, map[string]interface{}{"exception": field_delta},mymetric.Time())
									newAlarm.AddTag(p.TagName,p.fields_map[key].AlarmName)
									
			
									if p.fields_map[key].CopyTag {
										logPrintf("Copy Tags from original metric into monitoring metric")
										if len(p.fields_map[key].Tags) > 0 {
											logPrintf("Tags list is not empty - filetring tags")
											for _,v := range p.fields_map[key].Tags {
												if _, ok := a.tags[v]; ok{
													logPrintf("Copy Tags %s with value %s",v,a.tags[v])
													newAlarm.AddTag(v,a.tags[v])
												}
											}
										} else {
											logPrintf("Tags list is empty - copy all tags")
											for k,v := range a.tags {
												logPrintf("Copy Tags %s with value %s",k,v)
												newAlarm.AddTag(k,v)
											}
			
										}
									}
									alarmMetric = append(alarmMetric, newAlarm)
								}
							}
							
							// The cache is updated with the latest value
							logPrintf("Updating cache entry for metric with hashid %v", id)
							p.cache[id] = a						
						}
					case "delta_percent":
						logPrintf("Mode Delta Percent")
						if _, ok := p.cache[id]; !ok  {
							logPrintf("Creating cache entry for metric with hashid %v", id)
							p.cache[id] = a
						// If cached data are available then the rate is computed
						} else  {
							if lv, ok := p.cache[id].fields[key]; ok {

								field_delta_percent := ((value - lv) / lv) * 100

								switch p.fields_map[key].Operator {
								case "lt":
									if field_delta_percent < p.fields_map[key].Threshold {
										logPrintf("Threshold reached for field %s. %f < %f",key,field_delta_percent,p.fields_map[key].Threshold)
										thresholdReached = true 
									}
								case "gt":
									if field_delta_percent > p.fields_map[key].Threshold {
										logPrintf("Threshold reached for field %s. %f > %f",key,field_delta_percent,p.fields_map[key].Threshold)
										thresholdReached = true 
									}
								case "eq":
									if field_delta_percent == p.fields_map[key].Threshold {
										logPrintf("Threshold reached for field %s. %f == %f",key,field_delta_percent,p.fields_map[key].Threshold)
										thresholdReached = true 
									}
								} 
								if thresholdReached {
									newAlarm := metric.New(p.Measurement, map[string]string{}, map[string]interface{}{"exception": field_delta_percent},mymetric.Time())
									newAlarm.AddTag(p.TagName,p.fields_map[key].AlarmName)
									
			
									if p.fields_map[key].CopyTag {
										logPrintf("Copy Tags from original metric into monitoring metric")
										if len(p.fields_map[key].Tags) > 0 {
											logPrintf("Tags list is not empty - filetring tags")
											for _,v := range p.fields_map[key].Tags {
												if _, ok := a.tags[v]; ok{
													logPrintf("Copy Tags %s with value %s",v,a.tags[v])
													newAlarm.AddTag(v,a.tags[v])
												}
											}
										} else {
											logPrintf("Tags list is empty - copy all tags")
											for k,v := range a.tags {
												logPrintf("Copy Tags %s with value %s",k,v)
												newAlarm.AddTag(k,v)
											}
			
										}
									}
									alarmMetric = append(alarmMetric, newAlarm)
								}
							}
							
							// The cache is updated with the latest value
							logPrintf("Updating cache entry for metric with hashid %v", id)
							p.cache[id] = a						
						}
					case "delta_rate":
						logPrintf("Mode Delta Rate")
						if _, ok := p.cache[id]; !ok  {
							logPrintf("Creating cache entry for metric with hashid %v", id)
							p.cache[id] = a
						// If cached data are available then the rate is computed
						} else  {
							delta := mymetric.Time().Sub(p.cache[id].tm).Seconds()
							if lv, ok := p.cache[id].fields[key]; ok {
								field_rate := (value - lv)/float64(delta)
								switch p.fields_map[key].Operator {
								case "lt":
									if field_rate < p.fields_map[key].Threshold {
										logPrintf("Threshold reached for field %s. %f < %f",key,field_rate,p.fields_map[key].Threshold)
										thresholdReached = true 
									}
								case "gt":
									if field_rate > p.fields_map[key].Threshold {
										logPrintf("Threshold reached for field %s. %f > %f",key,field_rate,p.fields_map[key].Threshold)
										thresholdReached = true 
									}
								case "eq":
									if field_rate == p.fields_map[key].Threshold {
										logPrintf("Threshold reached for field %s. %f == %f",key,field_rate,p.fields_map[key].Threshold)
										thresholdReached = true 
									}
								}
								if thresholdReached {
									newAlarm := metric.New(p.Measurement, map[string]string{}, map[string]interface{}{"exception": field_rate},mymetric.Time())
									newAlarm.AddTag(p.TagName,p.fields_map[key].AlarmName)
			
									if p.fields_map[key].CopyTag {
										logPrintf("Copy Tags from original metric into monitoring metric")
										if len(p.fields_map[key].Tags) > 0 {
											logPrintf("Tags list is not empty - filetring tags")
											for _,v := range p.fields_map[key].Tags {
												if _, ok := a.tags[v]; ok{
													logPrintf("Copy Tags %s with value %s",v,a.tags[v])
													newAlarm.AddTag(v,a.tags[v])
												}
											}
										} else {
											logPrintf("Tags list is empty - copy all tags")
											for k,v := range a.tags {
												logPrintf("Copy Tags %s with value %s",k,v)
												newAlarm.AddTag(k,v)
											}
			
										}
									}
									alarmMetric = append(alarmMetric, newAlarm)
								}
							}
							// The cache is updated with the latest value
							logPrintf("Updating cache entry for metric with hashid %v", id)
							p.cache[id] = a	
						}
					}
				}
			}

		}
	}
	return append(metrics, alarmMetric...)
}

func logPrintf(format string, v...interface {}) {
    log.Printf("D! [processors.exception] " + format, v...)
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
    processors.Add("monitoring", func() telegraf.Processor {
        return &Monitoring {}
    })
}
