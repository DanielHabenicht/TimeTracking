@echo off
@REM Just comment in if you can't verify on your home assistant instance
@REM set cdate=%date:~-4%-%date:~7,2%-%date:~4,2%
@REM set ctime=%time:~0,2%:%time:~3,2%:%time:~6,2%
@REM if "%1"=="login" echo %cdate%: %username% - Logged in at %ctime% >> C:\tracking\userlog.txt
@REM if "%1"=="logout" echo %cdate%: %username% - Logged out at %ctime% >> C:\tracking\userlog.txt
@REM if "%1"=="locked" echo %cdate%: %username% - Locked at %ctime% >> C:\tracking\userlog.txt
@REM if "%1"=="unlocked" echo %cdate%: %username% - Unlocked at %ctime% >> C:\tracking\userlog.txt

if "%1"=="login" curl -i -X POST -H "Content-Type:application/json" -H "Authorization:Bearer YOUR_TOKEN" -d "{\"state\": \"True\" }" "https://example.de/api/states/sensor.pc_login"

if "%1"=="logout" curl curl -i -X POST -H "Content-Type:application/json" -H "Authorization:Bearer YOUR_TOKEN" -d "{\"state\": \"False\" }" "https://example.de/api/states/sensor.pc_login"

if "%1"=="locked" curl curl -i -X POST -H "Content-Type:application/json" -H "Authorization:Bearer YOUR_TOKEN" -d "{\"state\": \"False\" }" "https://example.de/api/states/sensor.pc_login"

if "%1"=="unlocked" curl curl -i -X POST -H "Content-Type:application/json" -H "Authorization:Bearer YOUR_TOKEN" -d "{\"state\": \"True\" }" "https://example.de/api/states/sensor.pc_login"
