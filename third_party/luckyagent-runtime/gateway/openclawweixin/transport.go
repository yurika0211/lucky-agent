// Package openclawweixin keeps source-compatible host bindings to the independent gateway module.
// Platform implementations live only in github.com/yurika0211/luckyagent-gateway.
package openclawweixin

import transport "github.com/yurika0211/luckyagent-gateway/openclawweixin"

type Adapter = transport.Adapter
type Config = transport.Config

var NewAdapter = transport.NewAdapter
