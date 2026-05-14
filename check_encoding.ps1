$p = "E:\temp\code\hubproxy\src\public\index.html"
$bytes = [System.IO.File]::ReadAllBytes($p)
$utf8 = [System.Text.Encoding]::UTF8
$str = $utf8.GetString($bytes)
if ($str.Contains("🚀")) { Write-Output "EMOJI: OK" } else { Write-Output "EMOJI: MISSING" }
if ($str.Contains("加速")) { Write-Output "ACCEL: OK" } else { Write-Output "ACCEL: MISSING" }
