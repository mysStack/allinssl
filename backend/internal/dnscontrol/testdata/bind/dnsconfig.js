var REG_NONE = NewRegistrar("none");
var DSP_BIND = NewDnsProvider("bind");

D("preview.example", REG_NONE, DnsProvider(DSP_BIND), DefaultTTL(300),
	SOA("@", "ns1.preview.example.", "hostmaster.preview.example.", 3600, 600, 604800, 300),
	NAMESERVER("ns1.preview.example."),
  A("ns1", "192.0.2.53"),
  A("www", "192.0.2.10")
);
