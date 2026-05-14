$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = "E:\temp\code\hubproxy\hubproxy_test.exe"
$psi.WorkingDirectory = "E:\temp\code\hubproxy"
$psi.UseShellExecute = $false; $psi.CreateNoWindow = $true
$p = [System.Diagnostics.Process]::Start($psi)
Start-Sleep -Seconds 4

function Get-Response($url) {
    try {
        $r = [System.Net.HttpWebRequest]::Create($url)
        $r.Timeout = 10000
        $r.ReadWriteTimeout = 10000
        $resp = $r.GetResponse()
        $sr = New-Object System.IO.StreamReader($resp.GetResponseStream())
        $body = $sr.ReadToEnd()
        return @{ Status = [int]$resp.StatusCode; Body = $body }
    } catch {
        $exc = $_.Exception
        if ($exc.Response) {
            $sr = New-Object System.IO.StreamReader($exc.Response.GetResponseStream())
            return @{ Status = [int]$exc.Response.StatusCode; Body = $sr.ReadToEnd() }
        }
        return @{ Status = 0; Body = "CONNECTION_FAILED: $($exc.Message)" }
    }
}

$j=0; $p=0; $fails=@()
function T($url, $desc) {
    $script:j++
    $r = Get-Response "http://localhost:5000$url"
    if ($r.Status -eq 200) {
        $script:p++
        Write-Output "PASS $($script.j): [200] $desc"
        # Show first 120 chars of response for verification
        $body = $r.Body
        if ($body.Length -gt 120) { $body = $body.Substring(0,120) + "..." }
        Write-Output "  -> $body"
    }
    elseif ($r.Status -eq 400) {
        if ($r.Body -match '"error"') {
            $script:p++
            Write-Output "PASS $($script.j): [400] $desc - structured error"
            Write-Output "  -> $($r.Body)"
        } else {
            $script:fails += "$($script.j): $desc - unexpected 400 body"
            Write-Output "FAIL $($script.j): [400] $desc"
            Write-Output "  -> $($r.Body)"
        }
    }
    elseif ($r.Status -eq 0) {
        Write-Output "INFO $($script.j): [no response] $desc - $($r.Body)"
    }
    else {
        $script:fails += "$($script.j): $desc - status $($r.Status)"
        Write-Output "FAIL $($script.j): [$($r.Status)] $desc"
        Write-Output "  -> $($r.Body)"
    }
}

# ==== TEST CASES ====
Write-Output "`n=== SEARCH API TESTS ==="

# 1. Basic single search
T "/search?q=nginx" "single search (basic)"

# 2. Pagination params  
T "/search?q=nginx&page=1&page_size=10" "pagination"

# 3. Source-specific
T "/search?q=nginx&source=docker.io" "source=docker.io"
T "/search?q=nginx&source=ghcr.io" "source=ghcr.io"

# 4. Multi-source modes
T "/search?q=nginx&multi_source=true" "multi_source (separate)"
T "/search?q=nginx&multi_source=true&merged=true" "multi_source+merged"

# 5. Search query validation
T "/search?q=" "empty query (should 400)"
T "/search?q=nginx&source=invalid_source" "invalid source (should 400)"

# 6. alt route
T "/search/multi?q=nginx" "search/multi route"

# 7. Tags
T "/tags/library/nginx?source=docker.io" "tags endpoint"

Write-Output "`n=== SUMMARY ==="
Write-Output "Passed: $p/$j"

if ($fails.Count -gt 0) {
    Write-Output "`nFAILURES:"
    $fails | ForEach-Object { Write-Output "  - $_" }
}

$p.Kill()
