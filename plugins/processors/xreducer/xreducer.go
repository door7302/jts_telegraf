package xreducer

import (
	"strings"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/processors"
)

type XReducer struct {
	Tags   []Match `toml:"tags"`
	Fields []Match `toml:"fields"`
}

type Match struct {
	Key string `toml:"key"`
}

const sampleConfig = `
  ## Tag and field xreducer
  # It all to keep only the most significant elem of an XPATH
  # /elem1/elem2/elem3 will be processed and only elem3 will be kept
  # we support per tag or field reducing
  # keyword "all" as key will reduce all 

  # [[processors.xreducer.tags]]
  #   ## field to reduce - ""All" will change all tags
  #   key = "value"


  # [[processors.xreducer.fields]]
  #   ## field to reduce - ""All" will change all fields
  #   key = "value"

`

func (r *XReducer) SampleConfig() string {
	return sampleConfig
}

func (r *XReducer) Description() string {
	return "Reduce Xpath in tag and field."
}

func (r *XReducer) Apply(metrics ...telegraf.Metric) []telegraf.Metric {
	for _, metric := range metrics {
		for _, elem := range r.Tags {
			for _, tag := range metric.TagList() {
				if tag.Key == elem.Key || elem.Key == "all" {
					if strings.Contains(tag.Key, "/") {
						parts := strings.Split(tag.Key, "/")
						tag.Key = parts[len(parts)-1]
					}
				}
			}
		}
		for _, elem := range r.Fields {
			for _, field := range metric.FieldList() {
				if field.Key == elem.Key || elem.Key == "all" {
					if strings.Contains(field.Key, "/") {
						parts := strings.Split(field.Key, "/")
						field.Key = parts[len(parts)-1]
					}
				}
			}
		}
	}
	return metrics
}

func init() {
	processors.Add("xreducer", func() telegraf.Processor {
		return &XReducer{}
	})
}
