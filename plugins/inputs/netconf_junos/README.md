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

  [[inputs.netconf_junos.subscription]]
    ## Name of the measurement that will be emitted
    name = "ifcounters"

    ## the JUNOS RPC to collect 
    junos_rpc = "<get-interface-information><statistics/></get-interface-information>"
  
    ## A list of xpath lite + type to collect / encode 
    ## Each entry in the list is made of:
    ## - xpath lite 
    ## - a type of encoding (supported types : int, float, string)
    ## 
    ## The xpath lite should follow the rpc reply XML document. Optional: you can include btw [] the KEY's name that must use to detect the loop 
    ## Only one loop field must be used and should be the same for all fields part of the same RPC 
    fields = ["/interface-information/physical-interface[ifname]/speed:string", 
            "/interface-information/physical-interface[ifname]/traffic-statistics/input-packets:int",
            "/interface-information/physical-interface[ifname]/traffic-statistics/output-packets:int",
            ]

    ## Interval to request the RPC
    sample_interval = "10s"
```