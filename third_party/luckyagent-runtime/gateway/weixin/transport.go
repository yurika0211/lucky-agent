// Package weixin keeps source-compatible host bindings to the independent gateway module.
// Platform implementations live only in github.com/yurika0211/luckyagent-gateway.
package weixin

import transport "github.com/yurika0211/luckyagent-gateway/weixin"

type Adapter = transport.Adapter
type Config = transport.Config

var DefaultConfig = transport.DefaultConfig

type LoginClient = transport.LoginClient

var NewAdapter = transport.NewAdapter
var NewLoginClient = transport.NewLoginClient

type QRCodeLogin = transport.QRCodeLogin
type Status = transport.Status
