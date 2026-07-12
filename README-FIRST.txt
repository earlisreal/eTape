eTape for Windows -- read this first
=====================================

eTape is a personal trading platform: a local app that reads market data and
renders candlestick charts, a Level 2 order-book ladder, and time & sales. It
runs entirely on your machine -- there is no cloud service, and nothing you
enter leaves this PC.

1. "Windows protected your PC" (SmartScreen) warning
-----------------------------------------------------
This build is not code-signed (no certificate -- this is a personal-use
release, not a commercial product), so Windows SmartScreen will warn you the
first time you run etape.exe:

    Windows protected your PC
    Microsoft Defender SmartScreen prevented an unrecognized app from
    starting.

Click "More info", then click "Run anyway". This only appears once per
downloaded copy of the binary.

2. Try the demo first (no setup needed)
-----------------------------------------
Double-click "etape-demo.cmd" in this folder. It launches etape.exe against a
live, self-generated synthetic market -- no moomoo OpenD, no broker account,
no credentials required. A browser tab opens automatically to
http://127.0.0.1:8686 with a year of warm chart history, a breathing DOM
ladder, and a moving scanner/movers board -- streaming continuously, not a
one-time replay. Which symbols move (and how) reshuffles every time you
launch it.

The engine keeps running in the background (look for the eTape icon in your
system tray) even if you close that browser tab -- reopen
http://127.0.0.1:8686 at any time to reconnect. Use the tray icon's "Quit" to
actually stop it.

3. Where your data lives
--------------------------
eTape stores everything under your user profile, never anywhere else:

    %USERPROFILE%\.eTape\config.toml       -- settings (venues, gates, etc.)
    %USERPROFILE%\.eTape\credentials.json  -- broker API keys (kept local, never synced)
    %USERPROFILE%\.eTape\etape.db          -- local market-data / order journal (SQLite)

Deleting the whole %USERPROFILE%\.eTape\ folder resets eTape to a clean
first-run state.

4. Going live (real market data / real orders)
-------------------------------------------------
The demo above uses no live data. To trade or watch real quotes:

  a. Install moomoo OpenD for Windows (the local gateway that talks to
     moomoo's servers) and unlock trading once inside the OpenD app itself.
     eTape never sees your OpenD trade password -- unlock happens in OpenD's
     own UI, not in eTape.
  b. Run etape.exe (not etape-demo.cmd) -- a browser tab opens to
     http://127.0.0.1:8686 same as the demo, now backed by live OpenD.
  c. Open the Venues & credentials settings panel inside the eTape UI and
     enter your broker API keys there (TradeZero / Alpaca / moomoo). Keys are
     written to %USERPROFILE%\.eTape\credentials.json and are never sent
     anywhere except the broker you configure them for.

That's it -- no installer, no admin rights, no external services.
