# jukem

jukem is a self-hosted jukebox appliance. MPD plays music through the sound
hardware of the machine jukem is installed on, and a web UI and REST API
control it. It ships as a signed Alpine apk for x86_64 and aarch64, and as a
Docker image built from that same package.

See [PLAN.md](PLAN.md) for the design notes. The full README is written in
the last build step.
