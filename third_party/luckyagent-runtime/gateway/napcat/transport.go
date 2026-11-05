// Package napcat keeps source-compatible host bindings to the independent gateway module.
// Platform implementations live only in github.com/yurika0211/luckyagent-gateway.
package napcat

import transport "github.com/yurika0211/luckyagent-gateway/napcat"

type Adapter = transport.Adapter
type Config = transport.Config

var DefaultConfig = transport.DefaultConfig

type GroupInfo = transport.GroupInfo
type GroupMessage = transport.GroupMessage

var NewAdapter = transport.NewAdapter
