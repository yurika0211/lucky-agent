// Package tool preserves LuckyAgent imports while implementations live in
// luckyagent-tools and internal/toolapp.
package tool

import (
	application "github.com/yurika0211/luckyagent-runtime/toolapp"
	toolkit "github.com/yurika0211/luckyagent-tools"
)

var AnnotateToolCall = toolkit.AnnotateToolCall
var AnnotateToolCallWithTemplates = toolkit.AnnotateToolCallWithTemplates

type ApprovalOption = toolkit.ApprovalOption

var AskUserTool = toolkit.AskUserTool

type AuditEntry = toolkit.AuditEntry
type AuditStats = toolkit.AuditStats

var CSVQueryTool = toolkit.CSVQueryTool
var CalculateTool = toolkit.CalculateTool

type CapabilitySummary = toolkit.CapabilitySummary
type CapabilityTool = toolkit.CapabilityTool

const CatBuiltin = toolkit.CatBuiltin
const CatDelegate = toolkit.CatDelegate
const CatMCP = toolkit.CatMCP
const CatSkill = toolkit.CatSkill

type Category = toolkit.Category

var CheckCollisions = toolkit.CheckCollisions
var CompressOutput = toolkit.CompressOutput
var CopyLocalDir = toolkit.CopyLocalDir
var CurrentTimeTool = toolkit.CurrentTimeTool
var DBSchemaTool = toolkit.DBSchemaTool
var DataQueryTool = toolkit.DataQueryTool
var DataTool = toolkit.DataTool
var DedupOutputs = toolkit.DedupOutputs
var DefaultFilesystemPolicy = toolkit.DefaultFilesystemPolicy
var DefaultInstallLimits = toolkit.DefaultInstallLimits
var DefaultOutputCompressConfig = toolkit.DefaultOutputCompressConfig
var DefaultSandboxConfig = toolkit.DefaultSandboxConfig
var DefaultTierConfigs = toolkit.DefaultTierConfigs
var DocumentReadTool = toolkit.DocumentReadTool

type ErrQuotaExceeded = toolkit.ErrQuotaExceeded
type ErrToolDenied = toolkit.ErrToolDenied
type ErrToolDisabled = toolkit.ErrToolDisabled
type ErrToolNotFound = toolkit.ErrToolNotFound
type ExecutionContext = toolkit.ExecutionContext

var ExtractArchive = toolkit.ExtractArchive
var ExtractDocumentText = toolkit.ExtractDocumentText
var FileDeleteTool = toolkit.FileDeleteTool
var FileListTool = toolkit.FileListTool
var FileMkdirTool = toolkit.FileMkdirTool
var FileMoveTool = toolkit.FileMoveTool
var FilePatchTool = toolkit.FilePatchTool
var FileReadTool = toolkit.FileReadTool
var FileWriteTool = toolkit.FileWriteTool

type FilesystemPolicy = toolkit.FilesystemPolicy

var FormatAuditEntry = toolkit.FormatAuditEntry

type Gateway = toolkit.Gateway
type GatewayResult = toolkit.GatewayResult

var HTTPRequestTool = toolkit.HTTPRequestTool

const InstallAborted = toolkit.InstallAborted
const InstallConfirmed = toolkit.InstallConfirmed
const InstallFailed = toolkit.InstallFailed

type InstallHistory = toolkit.InstallHistory
type InstallLimits = toolkit.InstallLimits
type InstallManager = toolkit.InstallManager
type InstallManagerConfig = toolkit.InstallManagerConfig

const InstallProbing = toolkit.InstallProbing
const InstallReady = toolkit.InstallReady

type InstallRecord = toolkit.InstallRecord

const InstallRejected = toolkit.InstallRejected
const InstallScanning = toolkit.InstallScanning

type InstallSource = toolkit.InstallSource

const InstallStaging = toolkit.InstallStaging

type InstallStore = toolkit.InstallStore
type Ledger = toolkit.Ledger

var LogTool = toolkit.LogTool

type MCPClient = toolkit.MCPClient
type MCPError = toolkit.MCPError
type MCPRequest = toolkit.MCPRequest
type MCPResponse = toolkit.MCPResponse
type MCPServerConfig = toolkit.MCPServerConfig
type MCPToolInfo = toolkit.MCPToolInfo

var MemoryHygieneTool = toolkit.MemoryHygieneTool
var MemoryTool = toolkit.MemoryTool
var MoveDir = toolkit.MoveDir
var NewGateway = toolkit.NewGateway
var NewInstallManager = toolkit.NewInstallManager
var NewInstallStore = toolkit.NewInstallStore
var NewMCPClient = toolkit.NewMCPClient
var NewRegistry = toolkit.NewRegistry
var NewSkillExecutor = toolkit.NewSkillExecutor
var NewSkillLoader = toolkit.NewSkillLoader
var NewSkillRegistry = toolkit.NewSkillRegistry
var NewSkillSandbox = toolkit.NewSkillSandbox
var NewSkillToolService = toolkit.NewSkillToolService
var NewSubscriptionManager = toolkit.NewSubscriptionManager
var NewToolRouter = toolkit.NewToolRouter
var NewTraceRecord = toolkit.NewTraceRecord
var NewTraceRecordWithTemplates = toolkit.NewTraceRecordWithTemplates
var NewUsageTracker = toolkit.NewUsageTracker
var NormalizeRoot = toolkit.NormalizeRoot

type Observation = toolkit.Observation
type OpenCLIConfig = toolkit.OpenCLIConfig

var OpenCLITool = toolkit.OpenCLITool

type OutputCompressConfig = toolkit.OutputCompressConfig

var ParallelCompressOutputs = toolkit.ParallelCompressOutputs

type Param = toolkit.Param

var ParseSubTier = toolkit.ParseSubTier

type PendingApproval = toolkit.PendingApproval

const PermApprove = toolkit.PermApprove
const PermAuto = toolkit.PermAuto
const PermDeny = toolkit.PermDeny

type PermissionLevel = toolkit.PermissionLevel
type Quota = toolkit.Quota

var RAGIndexTool = toolkit.RAGIndexTool
var RAGSearchTool = toolkit.RAGSearchTool
var RecallTool = toolkit.RecallTool

type Rect = toolkit.Rect

var RegisterMCPTools = toolkit.RegisterMCPTools
var RegisterSkillTools = toolkit.RegisterSkillTools

type Registry = toolkit.Registry

var RememberTool = toolkit.RememberTool
var RequestCredentialTool = toolkit.RequestCredentialTool

type RouteRule = toolkit.RouteRule

var SQLQueryTool = toolkit.SQLQueryTool

type SandboxConfig = toolkit.SandboxConfig
type SandboxError = toolkit.SandboxError
type ScanFinding = toolkit.ScanFinding
type ScanReport = toolkit.ScanReport

var ScanSkillDir = toolkit.ScanSkillDir

const SeverityBlock = toolkit.SeverityBlock
const SeverityInfo = toolkit.SeverityInfo
const SeverityWarn = toolkit.SeverityWarn

type ShellContext = toolkit.ShellContext
type SkillCandidate = toolkit.SkillCandidate

const SkillDisabled = toolkit.SkillDisabled
const SkillDiscovered = toolkit.SkillDiscovered

type SkillDocSection = toolkit.SkillDocSection

const SkillEnabled = toolkit.SkillEnabled
const SkillError = toolkit.SkillError

type SkillExecutor = toolkit.SkillExecutor
type SkillInfo = toolkit.SkillInfo

const SkillLoaded = toolkit.SkillLoaded

type SkillLoader = toolkit.SkillLoader
type SkillMetadata = toolkit.SkillMetadata
type SkillReadContent = toolkit.SkillReadContent

const SkillRegistered = toolkit.SkillRegistered

type SkillRegistry = toolkit.SkillRegistry
type SkillSandbox = toolkit.SkillSandbox
type SkillState = toolkit.SkillState
type SkillToolDef = toolkit.SkillToolDef
type SkillToolService = toolkit.SkillToolService

const SkillUnloaded = toolkit.SkillUnloaded
const SkillVersion = toolkit.SkillVersion
const SourceArchive = toolkit.SourceArchive
const SourcePath = toolkit.SourcePath
const SubBasic = toolkit.SubBasic
const SubEnterprise = toolkit.SubEnterprise
const SubFree = toolkit.SubFree
const SubPro = toolkit.SubPro

type SubTier = toolkit.SubTier
type SubscriptionManager = toolkit.SubscriptionManager

var TerminalTool = toolkit.TerminalTool

type TierConfig = toolkit.TierConfig

var ToOpenAIName = toolkit.ToOpenAIName

type Tool = toolkit.Tool
type ToolCallResult = toolkit.ToolCallResult
type ToolRouter = toolkit.ToolRouter
type TraceRecord = toolkit.TraceRecord

var TrialLoad = toolkit.TrialLoad

type TrialOptions = toolkit.TrialOptions
type TrialResult = toolkit.TrialResult

var TruncateOutput = toolkit.TruncateOutput
var TruncateOutputs = toolkit.TruncateOutputs

type UsageRecord = toolkit.UsageRecord
type UsageStats = toolkit.UsageStats
type UsageTracker = toolkit.UsageTracker
type UserSubscription = toolkit.UserSubscription

var ValidSkillName = toolkit.ValidSkillName
var WebFetchTool = toolkit.WebFetchTool

type WebSearchConfig = toolkit.WebSearchConfig

var WebSearchTool = toolkit.WebSearchTool

type AgentExecutorFunc = toolkit.AgentExecutorFunc
type AgentExecutorOptions = toolkit.AgentExecutorOptions
type AgentExecutorWithOptionsFunc = toolkit.AgentExecutorWithOptionsFunc
type ApprovalRequiredError = toolkit.ApprovalRequiredError
type AutonomyStartFunc = toolkit.AutonomyStartFunc
type AutonomyToolService = toolkit.AutonomyToolService
type BuiltinToolService = toolkit.BuiltinToolService
type CodexToolService = toolkit.CodexToolService
type ComputerManager = toolkit.ComputerManager
type ComputerUseConfig = toolkit.ComputerUseConfig
type ComputerUseToolService = toolkit.ComputerUseToolService
type CronSaveFunc = toolkit.CronSaveFunc
type CronTaskFactory = toolkit.CronTaskFactory
type CronToolService = toolkit.CronToolService

var DefaultDelegateConfig = toolkit.DefaultDelegateConfig
var DelegateCancelTool = toolkit.DelegateCancelTool

type DelegateConfig = toolkit.DelegateConfig

var DelegateExecutionSessionID = toolkit.DelegateExecutionSessionID

type DelegateManager = toolkit.DelegateManager
type DelegateTarget = toolkit.DelegateTarget
type DelegateTask = toolkit.DelegateTask

var DelegateTaskID = toolkit.DelegateTaskID
var DelegateTaskTool = toolkit.DelegateTaskTool
var DelegateToMCPTool = toolkit.DelegateToMCPTool
var DelegateToSkillTool = toolkit.DelegateToSkillTool

type EphemeralCredentialTerminalToolService = toolkit.EphemeralCredentialTerminalToolService
type EphemeralCredentialToolService = toolkit.EphemeralCredentialToolService

var ExtractDelegateWorkspace = toolkit.ExtractDelegateWorkspace

type GrokToolService = toolkit.GrokToolService
type HeartbeatToolService = toolkit.HeartbeatToolService

var ImageAnalyzeTool = toolkit.ImageAnalyzeTool
var ImageGenerateTool = toolkit.ImageGenerateTool

type ImageGenerationDefaults = toolkit.ImageGenerationDefaults

var ImageReadTool = toolkit.ImageReadTool
var ImageTool = toolkit.ImageTool
var ListTasksTool = toolkit.ListTasksTool

type MemoryToolService = toolkit.MemoryToolService
type NapCatCrossGroupReadConfig = toolkit.NapCatCrossGroupReadConfig
type NapCatGroupReader = toolkit.NapCatGroupReader

var NapCatReadGroupTool = toolkit.NapCatReadGroupTool
var NewAutonomyToolService = toolkit.NewAutonomyToolService
var NewBuiltinToolService = toolkit.NewBuiltinToolService
var NewCodexToolService = toolkit.NewCodexToolService
var NewComputerUseToolService = toolkit.NewComputerUseToolService
var NewCronToolService = toolkit.NewCronToolService
var NewDelegateManager = toolkit.NewDelegateManager
var NewEphemeralCredentialTerminalToolService = application.NewEphemeralCredentialTerminalToolService
var NewEphemeralCredentialToolService = application.NewEphemeralCredentialToolService
var NewGrokToolService = toolkit.NewGrokToolService
var NewHeartbeatToolService = toolkit.NewHeartbeatToolService
var NewMemoryToolService = toolkit.NewMemoryToolService
var NewPriorityTaskQueue = toolkit.NewPriorityTaskQueue
var NewRAGToolService = toolkit.NewRAGToolService
var NewServices = toolkit.NewServices
var NewTaskCache = toolkit.NewTaskCache

type ParallelDelegateResult = toolkit.ParallelDelegateResult
type ParallelDelegateTask = toolkit.ParallelDelegateTask

var ParseTaskPriority = toolkit.ParseTaskPriority

type PrioritizedTask = toolkit.PrioritizedTask

const PriorityCritical = toolkit.PriorityCritical
const PriorityHigh = toolkit.PriorityHigh
const PriorityLow = toolkit.PriorityLow
const PriorityNormal = toolkit.PriorityNormal

type PriorityTaskQueue = toolkit.PriorityTaskQueue
type RAGToolService = toolkit.RAGToolService

var RegisterBuiltinComputerTools = toolkit.RegisterBuiltinComputerTools
var RegisterBuiltinTools = toolkit.RegisterRuntimeBuiltinTools
var RegisterBuiltinToolsWithConfig = toolkit.RegisterRuntimeBuiltinToolsWithConfig

type Services = toolkit.Services

const StatusCancelled = toolkit.StatusCancelled
const StatusCompleted = toolkit.StatusCompleted
const StatusFailed = toolkit.StatusFailed
const StatusPending = toolkit.StatusPending
const StatusRunning = toolkit.StatusRunning

var SubagentTool = toolkit.SubagentTool

type TTSDefaults = toolkit.TTSDefaults

const TargetAgent = toolkit.TargetAgent
const TargetMCP = toolkit.TargetMCP
const TargetSkill = toolkit.TargetSkill

type TaskCache = toolkit.TaskCache
type TaskPriority = toolkit.TaskPriority
type TaskStatus = toolkit.TaskStatus

var TaskStatusTool = toolkit.TaskStatusTool
var TextToSpeechTool = toolkit.TextToSpeechTool
var WaitForTasksTool = toolkit.WaitForTasksTool
