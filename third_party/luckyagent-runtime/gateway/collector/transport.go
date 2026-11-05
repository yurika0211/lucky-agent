// Package collector keeps source-compatible host bindings to the independent gateway module.
// Platform implementations live only in github.com/yurika0211/luckyagent-gateway.
package collector

import transport "github.com/yurika0211/luckyagent-gateway/collector"

var ErrAlreadyActive = transport.ErrAlreadyActive
var ErrEmptyBatch = transport.ErrEmptyBatch
var ErrInactive = transport.ErrInactive
var ErrInvalidKey = transport.ErrInvalidKey
var KeyForMessage = transport.KeyForMessage

type Lucky = transport.Lucky
type LuckyAction = transport.LuckyAction

const LuckyActionCancel = transport.LuckyActionCancel
const LuckyActionOff = transport.LuckyActionOff
const LuckyActionOn = transport.LuckyActionOn
const LuckyActionStatus = transport.LuckyActionStatus
const LuckyActionUnknown = transport.LuckyActionUnknown

type LuckyBatch = transport.LuckyBatch
type LuckySegment = transport.LuckySegment
type LuckyStatus = transport.LuckyStatus

var NewLucky = transport.NewLucky
var NewLuckyWithClock = transport.NewLuckyWithClock
var ParseLuckyAction = transport.ParseLuckyAction
