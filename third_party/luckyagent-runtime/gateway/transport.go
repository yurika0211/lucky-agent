// Package gateway keeps source-compatible host bindings to the independent gateway module.
// Platform implementations live only in github.com/yurika0211/luckyagent-gateway.
package gateway

import transport "github.com/yurika0211/luckyagent-gateway"

var AcquireTelegramGatewayLease = transport.AcquireTelegramGatewayLease

type Attachment = transport.Attachment

const AttachmentAudio = transport.AttachmentAudio
const AttachmentDocument = transport.AttachmentDocument
const AttachmentImage = transport.AttachmentImage

type AttachmentType = transport.AttachmentType

const AttachmentVideo = transport.AttachmentVideo

type Chat = transport.Chat

const ChatChannel = transport.ChatChannel
const ChatGroup = transport.ChatGroup
const ChatPrivate = transport.ChatPrivate
const ChatSuperGroup = transport.ChatSuperGroup

type ChatType = transport.ChatType
type ForwardedMediaItem = transport.ForwardedMediaItem
type ForwardedMediaSender = transport.ForwardedMediaSender
type ForwardedTextSender = transport.ForwardedTextSender
type Gateway = transport.Gateway
type GatewayManager = transport.GatewayManager
type GatewayStats = transport.GatewayStats
type GatewayStatus = transport.GatewayStatus
type Message = transport.Message
type MessageHandler = transport.MessageHandler

var NewGatewayManager = transport.NewGatewayManager
var ReadRecentChatTarget = transport.ReadRecentChatTarget
var ReadSharedTelegramState = transport.ReadSharedTelegramState
var ReadTimeoutEvent = transport.ReadTimeoutEvent
var ReadTimeoutEventsSince = transport.ReadTimeoutEventsSince

type ReceiptGateway = transport.ReceiptGateway
type RecentChatTarget = transport.RecentChatTarget

var RecentChatTargetPath = transport.RecentChatTargetPath
var RestartGateway = transport.RestartGateway

type RestartOptions = transport.RestartOptions
type RestartResult = transport.RestartResult
type Restartable = transport.Restartable
type SentMessage = transport.SentMessage
type SharedTelegramState = transport.SharedTelegramState
type StreamGateway = transport.StreamGateway
type StreamSender = transport.StreamSender

var TelegramRuntimeStatePath = transport.TelegramRuntimeStatePath

type TimeoutEvent = transport.TimeoutEvent

var TimeoutEventPath = transport.TimeoutEventPath

type User = transport.User

var WriteRecentChatTarget = transport.WriteRecentChatTarget
var WriteSharedTelegramState = transport.WriteSharedTelegramState
var WriteTimeoutEvent = transport.WriteTimeoutEvent
