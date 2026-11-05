// Package turn aliases provider-independent contracts from the gateway module.
package turn

import transport "github.com/yurika0211/luckyagent-gateway/turn"

type ApprovalEvent = transport.ApprovalEvent
type Attachment = transport.Attachment

const AttachmentAudio = transport.AttachmentAudio
const AttachmentDocument = transport.AttachmentDocument
const AttachmentImage = transport.AttachmentImage

type AttachmentType = transport.AttachmentType

const AttachmentVideo = transport.AttachmentVideo

type ChatEvent = transport.ChatEvent

const ChatEventApprovalRequired = transport.ChatEventApprovalRequired
const ChatEventCompact = transport.ChatEventCompact
const ChatEventContent = transport.ChatEventContent
const ChatEventDone = transport.ChatEventDone
const ChatEventError = transport.ChatEventError
const ChatEventObservation = transport.ChatEventObservation
const ChatEventReasoningContent = transport.ChatEventReasoningContent
const ChatEventThinking = transport.ChatEventThinking
const ChatEventToolCall = transport.ChatEventToolCall
const ChatEventToolResult = transport.ChatEventToolResult

type ChatRuntime = transport.ChatRuntime
type CompactProgress = transport.CompactProgress
type EventType = transport.EventType
type Input = transport.Input
type ObservationEvent = transport.ObservationEvent
type Scope = transport.Scope

var TextInput = transport.TextInput

type TokenUsage = transport.TokenUsage
