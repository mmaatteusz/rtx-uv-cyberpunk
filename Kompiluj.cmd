@echo off
setlocal
pushd "%~dp0source" || exit /b 1
where go >nul 2>nul
if errorlevel 1 (
    echo Zainstaluj Go 1.24 lub nowsze i dodaj go.exe do PATH.
    popd
    exit /b 1
)
set "GOOS=windows"
set "GOARCH=amd64"
set "CGO_ENABLED=0"
go build -buildvcs=false -trimpath -ldflags "-s -w -H=windowsgui" -o "..\RTX-UV-Cyberpunk.exe" .
set "RTX_BUILD_STATUS=%ERRORLEVEL%"
popd
exit /b %RTX_BUILD_STATUS%
