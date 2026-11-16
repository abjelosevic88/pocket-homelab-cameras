# Reverse proxy

Set `BASE_URL` to the public address and `SECURE_COOKIES=true` behind HTTPS. See [deploy/reverse-proxy/Caddyfile](../deploy/reverse-proxy/Caddyfile).

- **Live view** is an MJPEG stream (`multipart/x-mixed-replace`) that stays open while you watch: turn response buffering off (`flush_interval -1` in Caddy, `proxy_buffering off` in nginx) and do not cut long responses (a stream ends by itself after 30 minutes and the page opens a new one).
- **Recordings** are served with `Range` requests (MP4), so seeking works through any proxy that passes `Range` along.
- **Cameras**: the server, not the browser, talks to the cameras, so they can stay on your home network (and should not be reachable from the internet).
- The **secret picture addresses** (`/s/<key>/snapshot.jpg`, `/s/<key>/live.mjpg`) need no sign-in: keep the app private (a VPN like Tailscale) if you use them.
