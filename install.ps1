# whatsapp-mcp installer for Windows (PowerShell)
# Usage: powershell -c "iex (iwr -Uri https://raw.githubusercontent.com/lncitador/whatsapp-mcp/main/install.ps1).Content"
$ErrorActionPreference = "Stop"

$Repo = "lncitador/whatsapp-mcp"
$BinDir = "$env:LOCALAPPDATA\whatsapp-mcp\bin"

# Resolve latest release tag
$release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers @{ "User-Agent" = "whatsapp-mcp-installer" }
$tag = $release.tag_name
$version = $tag -replace '^v', ''

# Download ZIP
$arch = "amd64"
$url = "https://github.com/$Repo/releases/download/$tag/whatsapp-mcp_${version}_windows_${arch}.zip"
$tmp = New-Item -ItemType Directory -Path ([System.IO.Path]::Combine([System.IO.Path]::GetTempPath(), "wmcp-install-$([System.Guid]::NewGuid().ToString('N'))"))

Write-Host "downloading whatsapp-mcp $tag (windows/$arch)..."
Invoke-WebRequest -Uri $url -OutFile "$tmp\wmcp.zip" -UseBasicParsing
Expand-Archive -Path "$tmp\wmcp.zip" -DestinationPath $tmp.FullName -Force

# Install binary
New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
Copy-Item "$tmp\whatsapp-mcp.exe" -Destination "$BinDir\whatsapp-mcp.exe" -Force
Remove-Item -Recurse -Force $tmp

# Add to PATH if needed
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$BinDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$BinDir", "User")
    $env:Path = "$env:Path;$BinDir"
    Write-Host "added $BinDir to PATH (restart terminal to take effect)"
}

$installedVersion = & "$BinDir\whatsapp-mcp.exe" --version 2>&1
Write-Host "installed: $BinDir\whatsapp-mcp ($installedVersion)"

# Install agent skill via npx if available
if (Get-Command npx -ErrorAction SilentlyContinue) {
    Write-Host "installing agent skill..."
    & npx -y skills@latest add "https://github.com/$Repo" --skill whatsapp --global -y *> $null
}

Write-Host ""
Write-Host "next steps:"
Write-Host "  claude mcp add whatsapp -- whatsapp-mcp stdio"
Write-Host "  # then call the auth_status tool (or run: whatsapp-mcp status) and scan the QR"
