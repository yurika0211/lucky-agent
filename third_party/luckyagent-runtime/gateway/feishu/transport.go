// Package feishu keeps source-compatible host bindings to the independent gateway module.
// Platform implementations live only in github.com/yurika0211/luckyagent-gateway.
package feishu

import transport "github.com/yurika0211/luckyagent-gateway/feishu"

type Adapter = transport.Adapter
type Config = transport.Config

var DefaultConfig = transport.DefaultConfig
var ErrUnsupportedMedia = transport.ErrUnsupportedMedia
var MediaDeliveryGuidance = transport.MediaDeliveryGuidance
var NewAdapter = transport.NewAdapter
