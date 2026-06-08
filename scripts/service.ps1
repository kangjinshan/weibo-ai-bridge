[CmdletBinding()]
param(
    [Parameter(Position = 0, Mandatory = $true)]
    [ValidateSet("install", "start", "stop", "restart", "status", "logs", "uninstall")]
    [string]$Command,

    [string]$ServiceName = $env:WEIBO_AI_BRIDGE_SERVICE_NAME,
    [string]$BinPath = $env:WEIBO_AI_BRIDGE_BIN,
    [string]$ConfigPath = $env:WEIBO_AI_BRIDGE_CONFIG_PATH,
    [string]$LogDir = $env:WEIBO_AI_BRIDGE_LOG_DIR,
    [System.Management.Automation.PSCredential]$Credential
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$ProjectName = "weibo-ai-bridge"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = Split-Path -Parent $ScriptDir

if ([string]::IsNullOrWhiteSpace($ServiceName)) {
    $ServiceName = $ProjectName
}

function Write-Info {
    param([string]$Message)
    Write-Host "[INFO] $Message" -ForegroundColor Cyan
}

function Write-Ok {
    param([string]$Message)
    Write-Host "[OK] $Message" -ForegroundColor Green
}

function Resolve-AbsolutePath {
    param([string]$Path)
    if ([string]::IsNullOrWhiteSpace($Path)) {
        return ""
    }
    return [System.IO.Path]::GetFullPath($Path)
}

function Get-DefaultBinPath {
    if (-not [string]::IsNullOrWhiteSpace($BinPath)) {
        return (Resolve-AbsolutePath $BinPath)
    }

    $candidate = Join-Path $RepoRoot "build\weibo-ai-bridge.exe"
    if (Test-Path -LiteralPath $candidate) {
        return (Resolve-AbsolutePath $candidate)
    }

    return (Resolve-AbsolutePath $candidate)
}

function Get-DefaultConfigPath {
    if (-not [string]::IsNullOrWhiteSpace($ConfigPath)) {
        return (Resolve-AbsolutePath $ConfigPath)
    }

    return (Resolve-AbsolutePath (Join-Path $RepoRoot "config\config.toml"))
}

function Get-DefaultLogDir {
    if (-not [string]::IsNullOrWhiteSpace($LogDir)) {
        return (Resolve-AbsolutePath $LogDir)
    }

    $programData = [Environment]::GetFolderPath("CommonApplicationData")
    if ([string]::IsNullOrWhiteSpace($programData)) {
        $programData = Join-Path $RepoRoot "tmp"
    }
    return (Resolve-AbsolutePath (Join-Path $programData $ProjectName))
}

function Assert-Administrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw "install/uninstall 需要以管理员身份运行 PowerShell"
    }
}

function Get-ServiceOrNull {
    param([string]$Name)
    return Get-Service -Name $Name -ErrorAction SilentlyContinue
}

function Invoke-Sc {
    param([string[]]$Arguments)
    $output = & sc.exe @Arguments 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "sc.exe $($Arguments -join ' ') failed: $output"
    }
    return $output
}

function Set-BridgeServiceEnvironment {
    param(
        [string]$Name,
        [string]$ResolvedConfigPath,
        [string]$ResolvedLogDir
    )

    New-Item -ItemType Directory -Force -Path $ResolvedLogDir | Out-Null

    $logPath = Join-Path $ResolvedLogDir "$ProjectName.log"
    $machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $pathParts = @($userPath, $machinePath) | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }
    $servicePath = ($pathParts -join ";")

    $envLines = @(
        "CONFIG_PATH=$ResolvedConfigPath",
        "LOG_OUTPUT=$logPath",
        "WEIBO_AI_BRIDGE_SERVICE_NAME=$Name"
    )
    if (-not [string]::IsNullOrWhiteSpace($servicePath)) {
        $envLines += "PATH=$servicePath"
    }

    $regPath = "HKLM:\SYSTEM\CurrentControlSet\Services\$Name"
    New-ItemProperty -Path $regPath -Name "Environment" -PropertyType MultiString -Value $envLines -Force | Out-Null
    Write-Info "服务环境变量已写入: $regPath"
    Write-Info "日志文件: $logPath"
}

function Install-BridgeService {
    Assert-Administrator

    $resolvedBin = Get-DefaultBinPath
    $resolvedConfig = Get-DefaultConfigPath
    $resolvedLogDir = Get-DefaultLogDir

    if (-not (Test-Path -LiteralPath $resolvedBin)) {
        throw "未找到可执行文件: $resolvedBin。请先运行: go build -o build\weibo-ai-bridge.exe .\cmd\server"
    }
    if (-not (Test-Path -LiteralPath $resolvedConfig)) {
        Write-Warning "配置文件不存在: $resolvedConfig（服务仍会尝试启动）"
    }

    $existing = Get-ServiceOrNull $ServiceName
    $binaryPathName = "`"$resolvedBin`""

    if ($null -eq $existing) {
        $params = @{
            Name           = $ServiceName
            BinaryPathName = $binaryPathName
            DisplayName    = "Weibo AI Bridge"
            StartupType    = "Automatic"
        }
        if ($null -ne $Credential) {
            $params.Credential = $Credential
        }
        New-Service @params | Out-Null
        Invoke-Sc -Arguments @("description", $ServiceName, "Weibo private-message bridge for local AI Agent CLIs") | Out-Null
        Write-Ok "Windows 服务已安装: $ServiceName"
    } else {
        Invoke-Sc -Arguments @("config", $ServiceName, "binPath=", $binaryPathName, "start=", "auto") | Out-Null
        Write-Ok "Windows 服务已更新: $ServiceName"
    }

    Set-BridgeServiceEnvironment -Name $ServiceName -ResolvedConfigPath $resolvedConfig -ResolvedLogDir $resolvedLogDir
}

function Start-BridgeService {
    $svc = Get-ServiceOrNull $ServiceName
    if ($null -eq $svc) {
        throw "服务未安装: $ServiceName"
    }
    if ($svc.Status -ne "Running") {
        Start-Service -Name $ServiceName
    }
    Write-Ok "已启动 $ServiceName"
}

function Stop-BridgeService {
    $svc = Get-ServiceOrNull $ServiceName
    if ($null -eq $svc) {
        throw "服务未安装: $ServiceName"
    }
    if ($svc.Status -ne "Stopped") {
        Stop-Service -Name $ServiceName
    }
    Write-Ok "已停止 $ServiceName"
}

function Restart-BridgeService {
    $svc = Get-ServiceOrNull $ServiceName
    if ($null -eq $svc) {
        throw "服务未安装: $ServiceName"
    }
    if ($svc.Status -eq "Running") {
        Restart-Service -Name $ServiceName
    } else {
        Start-Service -Name $ServiceName
    }
    Write-Ok "已重启 $ServiceName"
}

function Show-BridgeServiceStatus {
    $svc = Get-ServiceOrNull $ServiceName
    if ($null -eq $svc) {
        throw "服务未安装: $ServiceName"
    }
    $svc | Format-List Name, DisplayName, Status, StartType, ServiceType
}

function Show-BridgeServiceLogs {
    $resolvedLogDir = Get-DefaultLogDir
    $logPath = Join-Path $resolvedLogDir "$ProjectName.log"
    if (-not (Test-Path -LiteralPath $logPath)) {
        throw "日志文件不存在: $logPath"
    }
    Get-Content -LiteralPath $logPath -Tail 100 -Wait
}

function Uninstall-BridgeService {
    Assert-Administrator

    $svc = Get-ServiceOrNull $ServiceName
    if ($null -eq $svc) {
        Write-Ok "服务未安装，无需卸载: $ServiceName"
        return
    }
    if ($svc.Status -ne "Stopped") {
        Stop-Service -Name $ServiceName
        $svc.WaitForStatus("Stopped", [TimeSpan]::FromSeconds(30))
    }
    Invoke-Sc -Arguments @("delete", $ServiceName) | Out-Null
    Write-Ok "已卸载 $ServiceName"
}

switch ($Command) {
    "install" { Install-BridgeService }
    "start" { Start-BridgeService }
    "stop" { Stop-BridgeService }
    "restart" { Restart-BridgeService }
    "status" { Show-BridgeServiceStatus }
    "logs" { Show-BridgeServiceLogs }
    "uninstall" { Uninstall-BridgeService }
}
