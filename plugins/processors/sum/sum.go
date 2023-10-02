package sum

import (
	"log"
	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/processors"
)

var sampleConfig = `
## Sum sources values and put the result in a target field
[[processors.sum]]
[[processors.sum.fields]]
sources = ["a","b"]
target = "aplusb"
`

type Sum struct {
	Log   		telegraf.Logger
	Fields []compute    `toml:"fields"`
	}

type compute struct {
	Sources		[]string	`toml:"sources"`
	Target		string		`toml:"target"`
	}

func(p * Sum) SampleConfig() string {
    return sampleConfig
}

func(p * Sum) Description() string {
    return "Compute the sum"
}

func(p * Sum) Apply(metrics...telegraf.Metric)[] telegraf.Metric {
	for _, metric := range metrics {
		for _, compute := range p.Fields {
			result := float64(0)
			add_field := false
			for _, sum_field := range compute.Sources {
				logPrintf("Looking for %v field in metric",sum_field)
				if value, ok := metric.GetField(sum_field); ok {
					if f_value, ok := convert(value); ok {
						logPrintf("add %v",f_value)
						result = result + f_value
						add_field = true
					}
				}
			}
			if add_field {
				logPrintf("add field %v to metric with value %v",compute.Target,result)
				metric.AddField(compute.Target,result)
			}
		}
	}
	return metrics
}

func logPrintf(format string, v...interface {}) {
    log.Printf("D! [processors.sum] " + format, v...)
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
    processors.Add("sum", func() telegraf.Processor {
        return &Sum {}
    })
}