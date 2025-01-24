package enrichment

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/processors"
)

var sampleConfig = `
  ## Enrich with external Tags from an external json file set by EnrichFilePath.
  ##
  ## Conditionnal enrichment based on source tags already added by input plugin
  ## There are 2 levels of filtering. Level1 Source Tag ---> Level2 Source Tag ---> Tags to add
  ## If one level of filtering (default) is used the plugin looks for the wellknown level2
  ## Tag "LEVEL1TAGS" in the json file.
  ## The json file as read periodically every RefreshPeriod minutes. (by default 60m)
  ## See README file for more info about the Json file structure.
  ##
  enrichfilepath = ""
  twolevels = false
  refreshperiod = 60
  ## Filtering input tags
  ## Tags set by input plugin used as filter conditions
  ## Level2TagKey is only required when TwoLevel is set to true. 
  ## Level2tagkey is a list of tag that must match. if several level2 keys match, the tags will be merged
  level1tagkey = ""
  level2tagkey = []
`

var enrich map[string]map[string]map[string]string

type Enrichment struct {
	EnrichFilePath string   `toml:"enrichfilepath"`
	TwoLevels      bool     `toml:"twolevels"`
	RefreshPeriod  int      `toml:"refreshperiod"`
	Level1TagKey   string   `toml:"level1tagkey"`
	Level2TagKey   []string `toml:"level2tagkey"`

	initialized bool
	FileError   bool
	LastUpdate  time.Time
	CurrentHash string
}

func (p *Enrichment) SampleConfig() string {
	return sampleConfig
}

func (p *Enrichment) Description() string {
	return "Enrich with external tags based on existing tags"
}

func (p *Enrichment) Apply(metrics ...telegraf.Metric) []telegraf.Metric {
	currentTime := time.Now()
	delta := int(currentTime.Sub(p.LastUpdate).Minutes())
	if !p.initialized || delta >= p.RefreshPeriod {
		if p.RefreshPeriod <= 0 {
			p.RefreshPeriod = 60
		}
		update_db := false
		// Open enrichment file
		jsonFile, err := os.Open(p.EnrichFilePath)

		if err != nil {
			log.Printf("E! [processors.enrichment] Error when opening enrichment file %s error is %v", p.EnrichFilePath, err)
			p.FileError = true
			p.initialized = false
		} else {
			hash := md5.New()

			if _, err := io.Copy(hash, jsonFile); err != nil {
				logPrintf("Error during computing hash")
				update_db = true
			}
			defer jsonFile.Close()
			hashInBytes := hash.Sum(nil)[:16]
			MD5String := hex.EncodeToString(hashInBytes)
			if MD5String != p.CurrentHash {
				logPrintf("Hash is different than the previous one - update DB")
				p.CurrentHash = MD5String
				update_db = true
			} else {
				update_db = false

			}

		}
		if update_db {
			jsonFile, err := os.Open(p.EnrichFilePath)
			if err != nil {
				log.Printf("E! [processors.enrichment] Error when opening enrichment file %s error is %v", p.EnrichFilePath, err)
				p.FileError = true
				p.initialized = false
			} else {
				//reset DB
				enrich = make(map[string]map[string]map[string]string)
				byteValue, _ := ioutil.ReadAll(jsonFile)
				json.Unmarshal([]byte(byteValue), &enrich)
				p.FileError = false
				p.initialized = true
				p.LastUpdate = time.Now()
				defer jsonFile.Close()
			}

		} else {
			p.FileError = false
			p.initialized = true
			p.LastUpdate = time.Now()
		}

	}

	if !p.FileError {
		for _, metric := range metrics {
			CurrentTags := metric.Tags()
			Level1Tag := ""
			Level1Tag = CurrentTags[p.Level1TagKey]

			if Level1Tag != "" {
				// first add the Level 1 tags if present
				for tagKey, tagVal := range enrich[Level1Tag]["LEVEL1TAGS"] {
					if tagVal != "" {
						metric.AddTag(tagKey, string(tagVal))
					} else {
						metric.AddTag(tagKey, string(""))
					}
				}
				// if twolevels is set add level 2 tags if present
				if p.TwoLevels {
					for _, value := range p.Level2TagKey {
						Level2Tag := CurrentTags[value]
						for tagKey, tagVal := range enrich[Level1Tag][Level2Tag] {
							if tagVal != "" {
								metric.AddTag(tagKey, string(tagVal))
							} else {
								metric.AddTag(tagKey, string(""))
							}
						}
					}
				}
			}
		}
	}
	return metrics
}

func logPrintf(format string, v ...interface{}) {
	log.Printf("D! [processors.enrichment] "+format, v...)
}

func init() {
	processors.Add("enrichment", func() telegraf.Processor {
		return &Enrichment{}
	})
}
