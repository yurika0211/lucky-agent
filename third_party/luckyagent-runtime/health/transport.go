package health

import transport "github.com/yurika0211/luckyagent-api/health"

type CheckFunc = transport.CheckFunc

const CheckLiveness = transport.CheckLiveness
const CheckReadiness = transport.CheckReadiness

type CheckResult = transport.CheckResult
type CheckType = transport.CheckType

var FormatUptime = transport.FormatUptime

type HealthCheck = transport.HealthCheck
type HealthReport = transport.HealthReport

var NewHealthCheck = transport.NewHealthCheck

type Status = transport.Status

const StatusDegraded = transport.StatusDegraded
const StatusHealthy = transport.StatusHealthy
const StatusUnhealthy = transport.StatusUnhealthy

type SystemInfo = transport.SystemInfo
