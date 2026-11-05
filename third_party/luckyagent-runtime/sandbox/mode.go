package sandbox

import sandbox "github.com/yurika0211/luckyagent-tools/sandbox"

type Manager = sandbox.Manager
type Mode = sandbox.Mode

const ModeDev = sandbox.ModeDev
const ModeIso = sandbox.ModeIso
const ModeWorkspaceWrite = sandbox.ModeWorkspaceWrite

var NewManager = sandbox.NewManager
var Prepare = sandbox.Prepare

type Snapshot = sandbox.Snapshot

var SnapshotFromContext = sandbox.SnapshotFromContext
var WithSnapshot = sandbox.WithSnapshot
