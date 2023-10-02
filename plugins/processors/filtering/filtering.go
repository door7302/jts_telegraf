package filtering

import (
	"regexp"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/processors"
)

type Filtering struct {
	Tags       []rule
	Fields     []rule
	regexCache map[string]*regexp.Regexp
}

type rule struct {
	Key         string
	Pattern     string
	Action      string
}

const sampleConfig = `
  ## Tag and field filtering
  # if Drop is set = drop these metrics - forward others
  # if Accept is set = Accept these metrics - drop others
  # Once a metric is flagged to be dropped it can't be accept by a successive filter

  # Only STRINGS fields are supported
  # [[processors.filtering.tags]]
  #   ## Tag to change
  #   key = "value"
  #   pattern = "^(\\d)\\d\\d$"
  #   Action = "drop|accept"

  # [[processors.filtering.fields]]
  #   ## Tag to change
  #   key = "value"
  #   pattern = "^(\\d)\\d\\d$"
  #   Action = "drop|accept"
`
func NewFiler() *Filtering {
	return &Filtering{
		regexCache: make(map[string]*regexp.Regexp),
	}
}

func (r *Filtering) SampleConfig() string {
	return sampleConfig
}

func (r *Filtering) Description() string {
	return "Filter tag and field values with Filtering pattern"
}

// Remove single item from slice
func remove(slice []telegraf.Metric, i int) []telegraf.Metric {
	slice[len(slice)-1], slice[i] = slice[i], slice[len(slice)-1]
	return slice[:len(slice)-1]
}

func (r *Filtering) Apply(metrics ...telegraf.Metric) []telegraf.Metric {
	metric_to_drop := false
	for idx, metric := range metrics {
		metric_to_drop = false
		for _, rule := range r.Tags {
			if value, ok := metric.GetTag(rule.Key); ok {
				if r.checkregex(rule, value) {
					if rule.Action == "drop" {
						metric_to_drop= true
					}
				} else {
					if rule.Action == "accept" {
						metric_to_drop= true
					}
				}
			}
		}
		for _, rule := range r.Fields {
			if value, ok := metric.GetField(rule.Key); ok {
				switch value := value.(type) {
				case string:
					if r.checkregex(rule, value) {
						if rule.Action == "drop" {
							metric_to_drop= true
						}
					} else {
						if rule.Action == "accept" {
							metric_to_drop= true
						}
					}
				}
			}
		}

		if metric_to_drop {
			metrics = remove(metrics, idx)
		}

	}
	return metrics
}

func (r *Filtering) checkregex(c rule, src string) (bool) {
	regex, compiled := r.regexCache[c.Pattern]
	if !compiled {
		regex = regexp.MustCompile(c.Pattern)
		r.regexCache[c.Pattern] = regex
	}

	found := false
	if regex.MatchString(src) {
		found = true
	}

	return found
}

func init() {
	processors.Add("filtering", func() telegraf.Processor {
		return NewFiler()
	})
}

