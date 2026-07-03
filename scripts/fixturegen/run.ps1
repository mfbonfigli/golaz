# Build the fixture-generator image, run it, and copy the generated fixtures
# into the golaz testdata tree. (Windows PowerShell 5.1 compatible.)
$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = (Resolve-Path (Join-Path $ScriptDir "..\..")).Path
$TestData = Join-Path $RepoRoot "internal\laz\testdata"
$Out = Join-Path $ScriptDir "out"

New-Item -ItemType Directory -Force $Out | Out-Null

Write-Host "== building image =="
docker build -t golaz-fixturegen $ScriptDir
if ($LASTEXITCODE -ne 0) { throw "docker build failed" }

Write-Host "== generating fixtures =="
docker run --rm `
  -v "$(Join-Path $TestData 'las'):/in:ro" `
  -v "${Out}:/out" `
  golaz-fixturegen
if ($LASTEXITCODE -ne 0) { throw "fixture generation failed" }

Write-Host "== placing fixtures into testdata =="
New-Item -ItemType Directory -Force (Join-Path $TestData "cpporacle\corrupt") | Out-Null
New-Item -ItemType Directory -Force (Join-Path $TestData "cpporacle\compat") | Out-Null
Copy-Item (Join-Path $Out "las\*.las") (Join-Path $TestData "las") -Force -Verbose
Copy-Item (Join-Path $Out "las\*.laz") (Join-Path $TestData "las") -Force -Verbose
Copy-Item (Join-Path $Out "cpporacle\*.las") (Join-Path $TestData "cpporacle") -Force -Verbose
Copy-Item (Join-Path $Out "cpporacle\*.laz") (Join-Path $TestData "cpporacle") -Force -Verbose
Copy-Item (Join-Path $Out "cpporacle\*.csv") (Join-Path $TestData "cpporacle") -Force -Verbose
Copy-Item (Join-Path $Out "cpporacle\corrupt\*.laz") (Join-Path $TestData "cpporacle\corrupt") -Force -Verbose
Copy-Item (Join-Path $Out "cpporacle\corrupt\*.json") (Join-Path $TestData "cpporacle\corrupt") -Force -Verbose
Copy-Item (Join-Path $Out "cpporacle\compat\*.las") (Join-Path $TestData "cpporacle\compat") -Force -Verbose
Copy-Item (Join-Path $Out "cpporacle\compat\*.laz") (Join-Path $TestData "cpporacle\compat") -Force -Verbose
Copy-Item (Join-Path $Out "cpporacle\compat\*.csv") (Join-Path $TestData "cpporacle\compat") -Force -Verbose
Copy-Item (Join-Path $Out "cpporacle\compat\*.json") (Join-Path $TestData "cpporacle\compat") -Force -Verbose

Write-Host "== done =="
