# Sets up a demo tenant for the Docker setup: endpoints that succeed, retry,
# fail in realistic ways, and verify signatures. Publishes a few events and
# copies the API key to the clipboard for the dashboard sign-in.
#
#   powershell -ExecutionPolicy Bypass -File scripts\demo.ps1
param(
  [string]$Api = "http://127.0.0.1:8080",
  [string]$Receiver = "http://mockreceiver:8090",   # as the workers see it, inside Docker
  [string]$ReceiverFromHost = "http://127.0.0.1:8090",
  [string]$AdminToken = "dev-admin-token"
)
$ErrorActionPreference = "Stop"

$t = Invoke-RestMethod -Method Post -Uri "$Api/v1/tenants" `
  -Headers @{ Authorization = "Bearer $AdminToken" } `
  -ContentType "application/json" -Body '{"name":"demo"}'
$h = @{ Authorization = "Bearer $($t.api_key)" }

function Add-Endpoint([string]$Path) {
  $body = @{ url = "$Receiver$Path"; event_types = @("invoice.paid") } | ConvertTo-Json
  Invoke-RestMethod -Method Post -Uri "$Api/v1/endpoints" -Headers $h -ContentType "application/json" -Body $body
}
function Add-Failing([int]$Code, [string]$Text) {
  Add-Endpoint ("/respond?code=$Code&body=" + [uri]::EscapeDataString($Text)) | Out-Null
}

Add-Endpoint "/ok" | Out-Null
Add-Endpoint "/flaky?fail=2" | Out-Null
Add-Endpoint "/status/410" | Out-Null
$verify = Add-Endpoint "/verify"
Invoke-RestMethod -Method Put -Uri "$ReceiverFromHost/_secret" -Body $verify.secret | Out-Null
Add-Failing 500 "SignatureVerificationError: No signatures found matching the expected signature for payload"
Add-Failing 403 "<title>Attention Required! | Cloudflare</title> Sorry, you have been blocked"
Add-Failing 400 '{"error":"webhook timestamp too old","tolerance_seconds":300}'

foreach ($i in 1..3) {
  $body = @{ event_type = "invoice.paid"; payload = @{ invoice_id = "inv_$i"; amount = 1000 * $i } } | ConvertTo-Json
  Invoke-RestMethod -Method Post -Uri "$Api/v1/events" -Headers $h -ContentType "application/json" -Body $body | Out-Null
}

$t.api_key | Set-Clipboard
Write-Host ""
Write-Host "Demo tenant ready: 7 endpoints, 3 events (21 deliveries)."
Write-Host "API key copied to the clipboard. Sign in at http://127.0.0.1:8081"
Write-Host "Failing deliveries reach 'Failed deliveries' in about 4 minutes, then get diagnosed automatically."
