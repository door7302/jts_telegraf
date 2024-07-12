# Netconf_JUNOS Input Plugin

This plugin consumes Netconf data coming from Juniper (Junos/EVO) devices

## Configuration

```toml
[[inputs.netconf_junos]]
  ## Address of the Juniper NETCONF server
  addresses = ["10.49.234.114"]

  ## define credentials
  username = "lab"
  password = "lab123"

  ## redial in case of failures after
  redial = "10s"

  ## Time Layout for epoch convertion - specify a sample Date/Time layout - default layout is the following:
  time_layout = "2006-01-02 15:04:05 MST"

  [[inputs.netconf_junos.subscription]]
    ## Name of the measurement that will be emitted
    name = "ifcounters"

    ## the JUNOS RPC to collect 
    junos_rpc = "<get-interface-information><statistics/></get-interface-information>"
  
    ## A list of xpath lite + type to collect / encode 
    ## Each entry in the list is made of: <xpath>:<type>
    ## - xpath lite 
    ## a type of encoding (supported types : int, float, string, epoch, epoch_ms, epoch_us, epoch_ns)
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
```