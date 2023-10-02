package xmetrictags

import (
	"log"
	"time"
	"hash/fnv"

    "github.com/influxdata/telegraf"
    "github.com/influxdata/telegraf/plugins/processors"
)

var sampleConfig = `
[[processor.xmetrictags]]
[[processor.xmetrictags.field]]
track_key = "parent_ae_name"
tag_keys = ["device","if_name"]
tag_name = "lag_id"
`

type Xmetrictags struct {
	Log   		telegraf.Logger
	Fields []xmetric    `toml:"field"`
	Tags   []xmetric    `toml:"tag"`
	Period		string		`toml:"period"`
	initialized bool
	cache       map[uint64]compute
	last_cleared	time.Time
	}

type xmetric struct {
	Track_key	string	`toml:"track_key"`
	Tag_keys	[]string `toml:"tag_keys"`
	Tag_name	string	`toml:"tag_name"`
	Retention 	string	`toml:"retention"`
	}

type compute struct {
	tm time.Time
	track_key_value string
}

func(p * Xmetrictags) SampleConfig() string {
    return sampleConfig
}

func(p * Xmetrictags) Description() string {
    return "Take field or tag from a metric and add as a tag to other metric"
}

func hash(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

func(p * Xmetrictags) Apply(metrics...telegraf.Metric)[] telegraf.Metric {
	t_period,_ := time.ParseDuration(p.Period)
	if !p.initialized{
		logPrintf("Initializing xmetric...")
		p.cache = make(map[uint64]compute)
		p.initialized = true
		p.last_cleared = time.Now()
	}
	if time.Now().After(p.last_cleared.Add(t_period)) {
		nb_deleted := 0
		logPrintf("Time to clean the cache, nb cache entries %v",len(p.cache))			
		for k,v := range p.cache {
			logPrintf("Hashid %v time %v",k,v.tm)
			if time.Now().After(v.tm) {
				logPrintf("delete entry %v from cache",k)
				delete(p.cache,k)
				nb_deleted +=1
			}
	}
		logPrintf("%v entries deleted from cache",nb_deleted)
		p.last_cleared = time.Now()
	}
	for _, metric := range metrics {
		for _, xmetric_field := range p.Fields {
			t_retention, _ := time.ParseDuration(xmetric_field.Retention)
			hash_string := xmetric_field.Track_key
			hastags := false
			for _, tag := range xmetric_field.Tag_keys {
				logPrintf("Check if metric has tag %s",tag)
				if hastag := metric.HasTag(tag); !hastag{
					hastags = false
					break
				}
				if value, hastag := metric.GetTag(tag); hastag{
					hash_string = hash_string+value
					hastags = true
				}
			}
			// La metric dispose des tags et du track_key, on met la donnée dans le cache
			if value, ok := metric.GetField(xmetric_field.Track_key); ok && hastags{
				str_value := value.(string)
				if str_value != "" {
					id := hash(hash_string)
					a := compute {
						tm:	time.Now().Add(t_retention),
						track_key_value: str_value,
					}
					logPrintf("Cache entry with id %v updated with value %v",id,str_value)
					p.cache[id] = a
					metric.AddTag(xmetric_field.Tag_name,p.cache[id].track_key_value)
				} else {
					logPrintf("Metric with hash_string %s has an empty track_key value",hash_string)
				}
			}
			// la metric n'a pas le champ mais dispose des tags, on doit lui ajouter l'info si elle est dans le cache
			if _, ok := metric.GetField(xmetric_field.Track_key); !ok && hastags {
				id := hash(hash_string)
				if _, ok := p.cache[id]; ok {
					logPrintf("Metric needs the tag %s with value %s",xmetric_field.Tag_name,p.cache[id].track_key_value)
					metric.AddTag(xmetric_field.Tag_name,p.cache[id].track_key_value)
				}
			} 
		}
		for _, xmetric_tag := range p.Tags {
			t_retention, _ := time.ParseDuration(xmetric_tag.Retention)
			hash_string := xmetric_tag.Track_key
			hastags := false
			for _, tag := range xmetric_tag.Tag_keys {
				logPrintf("Check if metric has tag %s",tag)
				if hastag := metric.HasTag(tag); !hastag{
					hastags = false
					break
				}
				if value, hastag := metric.GetTag(tag); hastag{
					hash_string = hash_string+value
					hastags = true
				}
			}
			// La metric dispose des tags et du track_key, on met la donnée dans le cache
			if str_value, ok := metric.GetTag(xmetric_tag.Track_key); ok && hastags{
				if str_value != "" {
					id := hash(hash_string)
					a := compute {
						tm:	time.Now().Add(t_retention),
						track_key_value: str_value,
					}
					logPrintf("Cache entry with id %v updated with value %v",id,str_value)
					p.cache[id] = a
					metric.AddTag(xmetric_tag.Tag_name,p.cache[id].track_key_value)
				} else {
					logPrintf("Metric with hash_string %s has an empty track_key value",hash_string)
				}
			}
			// la metric n'a pas le champ mais dispose des tags, on doit lui ajouter l'info si elle est dans le cache
			if _, ok := metric.GetTag(xmetric_tag.Track_key); !ok && hastags {
				id := hash(hash_string)
				if _, ok := p.cache[id]; ok {
					logPrintf("Metric needs the tag %s with value %s",xmetric_tag.Tag_name,p.cache[id].track_key_value)
					metric.AddTag(xmetric_tag.Tag_name,p.cache[id].track_key_value)
				}
			} 
		}
	}
	return metrics
}	

func logPrintf(format string, v...interface {}) {
    log.Printf("D! [processors.Xmetrictags] " + format, v...)
}

func init() {
    processors.Add("xmetrictags", func() telegraf.Processor {
        return &Xmetrictags {}
    })
}
