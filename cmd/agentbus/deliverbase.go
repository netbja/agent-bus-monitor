package main

import (
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// projectDeliveryBase is the branch project delivers to, and where that
// comes from (dev-branch-flow §4.1): the template's [delivery] base, else the
// machine's delivery.base. deliver's --base is above both; missions and
// config --project read this.
func projectDeliveryBase(getenv func(string) string, project string) (base, source string, err error) {
	b, ok, err := rolecfg.DeliveryBase(getenv, project)
	if err != nil {
		return "", "", err
	}
	if ok {
		return b, "template " + rolecfg.TemplatePath(getenv, project), nil
	}
	r := config.Machine(getenv).Get("delivery.base")
	return r.Value.(string), r.Source, nil
}
