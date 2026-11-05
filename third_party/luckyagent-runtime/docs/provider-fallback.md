# Provider fallback

A fallback chain tries configured models in order. Each node is identified by
provider name, model, endpoint and a short credential hash. Logs print those
four fields and never print the credential itself.

## Model errors

`model_not_found`, `model not found`, HTTP 404 and
`not supported by any configured account` cool down that model for 30 minutes.
The same provider's other models stay available. After the cooldown the chain
allows one probe. Success clears the cooldown. Another model error starts it
again. The node is not closed for the life of the process.

## Shared endpoints

When a later node uses the same non-empty endpoint and credential, network, TLS
and 429 errors stay on the current node. They do not switch models. A node
without an endpoint stays independent. A model-level error still switches to a
node with a different model.

Nodes with different endpoints remain independent. Three consecutive site-wide
failures cool that node for five minutes.

## Circuit breaker

The configured circuit breaker is installed on each node. It counts that node's
failures and does not wrap the whole chain. A fallback failure therefore does
not block the next probe of another node. A chain that is already cooling a
model does not add a second chain-level penalty.

## Health

`health.FallbackCheck` reads `FallbackChain.Status`. Hosts register it on the
readiness and detail health checker. The existing check result has no detail
object, so the node list is reported in the check error text: index, provider,
model, endpoint, credential hash, cooldown and circuit state. Liveness stays a
process check. A missing chain is degraded. Every node cooling or open is
unhealthy. One available node is degraded while another node is cooling.
