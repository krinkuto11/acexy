# `acexy` - An AceStream Proxy Written In Go! ⚡

[![Go Build](https://github.com/Javinator9889/acexy/actions/workflows/build.yaml/badge.svg)](https://github.com/Javinator9889/acexy/actions/workflows/build.yaml)
[![Docker Release](https://github.com/Javinator9889/acexy/actions/workflows/release.yaml/badge.svg?event=release)](https://github.com/Javinator9889/acexy/actions/workflows/release.yaml)

## Table of Contents

- [How It Works? 🛠](#how-it-works-)
- [Key Features 🔗](#key-features-)
- [Usage 📐](#usage-)
- [Optimizing 🚀](#optimizing-)
  - [Alternative 🧃](#alternative-)
- [Configuration Options ⚙](#configuration-options-)

## How It Works? 🛠

This project is a wrapper around the
[AceStream middleware HTTP API](https://docs.acestream.net/developers/start-playback/#using-middleware), allowing both
[HLS](https://en.wikipedia.org/wiki/HTTP_Live_Streaming) and
[MPEG-TS](https://en.wikipedia.org/wiki/HTTP_Live_Streaming) playback
of a stream.

I was tired of the limitations of AceStream and some of the problems that 
exist when playing a stream 📽. For example, it is only possible to play
the same channel for **1 single client**. For having multiple clients
playing **different streams**, you must manually add a unique `pid` per 
client. If there was an error during the transmission, the **whole stream
goes down**, etc.

I found quite frustrating the experience of using AceStream in a home network
with a single server and multiple clients, to try to optimize resources. This
is the topology for which I am using AceStream:

![AceStream Topology For My Network](doc/img/topology.svg)

There are some problems:

* Only **one client** can play the same stream at a time 🚫.
* Having each client to run AceStream on their own is a waste of resources
  and saturates the network 📉.
* Multiple clients can play different streams if they have a unique `pid`
  (Player ID) associated 🔓.
* The standard AceStream HTTP API is not resilient enough against errors,
  if the transmission stops it stops for every client ❌.

## Key Features 🔗

When using `acexy`, you automatically have:

* A single, centralized server running **all your AceStream streams** ⛓.
* Automatic assignation of a unique `pid` (Player ID) **per client per stream** 🪪.
* **Stream Multiplexing** 🕎: The same stream can be reproduced *at the
  same time in multiple clients*.
* **Resilient, error-proof** streaming thanks to the HTTP Middleware 🛡.
* *Blazing fast, minimal proxy* ☄ written in Go!
* **Orchestrator Integration** 🎛: Dynamic engine management and load balancing

With this proxy, the following architecture is now possible:

![acexy Topology](doc/img/acexy.svg)

### New with Orchestrator Integration 🆕

With the built-in orchestrator integration, acexy now supports:

* **Dynamic Engine Pools**: Automatically manages multiple acestream engines
* **Intelligent Load Balancing**: One stream per engine with automatic provisioning  
* **High Availability**: Automatic failover and engine replacement
* **Zero Manual Configuration**: Engines are provisioned on-demand

## Usage 📐

`acexy` is available and published as a Docker image. Make sure you have
the latest [Docker](https://docker.com) image installed and available.

**Recommended Setup (with Orchestrator)**: The acexy container will work with 
the orchestrator to automatically manage acestream engines. This is the recommended
approach for production deployments as it provides load balancing and high availability.

**Legacy Setup**: The acexy container can also connect directly to a single AceStream 
server for backwards compatibility.

> **INFO**: There is a `docker-compose.yml` file in the repo you can directly
> use to launch the whole block. This is **the recommended setup starting
> from `v0.2.0`**.

To run the services block, first grab the `docker-compose.yml` file, and run:

```shell
wget https://raw.githubusercontent.com/Javinator9889/acexy/refs/heads/main/docker-compose.yml
docker compose run -d
```

If you don't want to use Docker Compose, assuming you already have an
AceStream server, another way could be:

```shell
docker run --network host ghcr.io/javinator9889/acexy
```

> **NOTE**: For your convenience, a `docker-compose.yml` file is given with
> all the possible adjustable parameters. It should be ready to run, and it's
> the recommended way starting from `v0.2.0`.

By default, the proxy will work in MPEG-TS mode. For switching between them,
you must add the **`-m3u8` flag** or set **`ACEXY_M3U8=true` environment
variable**.

> **NOTE**: The HLS mode - `ACEXY_M3U8` or `-m3u8` flag - is in a non-tested
> status. Using it is discouraged and not guaranteed to work.

There is a single available endpoint: `/ace/getstream` which takes the same
parameters as the standard
[AceStream Middleware/HTTP API](https://docs.acestream.net/developers/api-reference/). Therefore,
for running a stream, just open the following link in your preferred application - such as VLC:

```
http://127.0.0.1:8080/ace/getstream?id=dd1e67078381739d14beca697356ab76d49d1a2
```

where `dd1e67078381739d14beca697356ab76d49d1a2` is the ID of the AceStream 
channel.

## Optimizing 🚀

The AceStream Engine running behind of the proxy has a number of ports that can
be exposed to optimize the performance. Those are, by default:

- `8621/tcp`
- `8621/udp`

> NOTE: They can be adjusted through the `EXTRA_FLAGS` variable - within Docker - by
> using the `--port` flag.

Exposing those ports should help getting a more stable streaming experience. Notice
that you will need to open up those ports on your gateway too.

For reference, this is how you should run the Docker command:

```shell
docker run -t -p 8080:8080 -p 8621:8621 ghcr.io/javinator9889/acexy
```

### Alternative 🧃

AceStream underneath attempts to use UPnP IGD to connect against a remote machine.
The problem is that this is not working because of the bridging layer added by Docker
(see: https://docs.docker.com/engine/network/drivers/bridge/).

If you are running a single instance of Acexy - and a single instance of AceStream -
it should be safe for you to run the container with *host networking*. This means:

- The container **can access** any other application bridged to your main network.
- You **don't need** to expose any ports.
- Performance **is optimized** a little bit.

> NOTE: This only works on Linux environments. See https://docs.docker.com/engine/network/drivers/host/
> for more information.

The command is quite straightforward:

```shell
docker run -t --network host ghcr.io/javinator9889/acexy
```

That should enable AceStream to use UPnP freely.

## Orchestrator Integration 🎛

Starting from version `v0.3.0`, acexy includes built-in integration with the acestream-orchestrator for automatic load balancing and engine management. This provides several key benefits:

### Key Features 🔗

* **Dynamic Engine Selection** 🎯: Automatically selects the best available engine for each stream
* **Configurable Load Balancing** ⚖️: Configurable maximum streams per engine with empty engine prioritization  
* **Auto-Provisioning** 🏭: Automatically provisions new engines when needed
* **High Availability** 🛡: Graceful fallback to configured engine if orchestrator is unavailable
* **Zero Configuration** ⚡: Works out-of-the-box with docker-compose setup

### How It Works 🛠

1. **Stream Request**: Client requests a stream from acexy
2. **Engine Selection**: acexy queries orchestrator for available engines
3. **Load Balancing**: Prioritizes empty engines, then engines with fewest streams within configured maximum
4. **Auto-Provision**: If no engines have capacity, provisions a new acestream container
5. **Stream Serving**: Serves stream from selected/provisioned engine
6. **Event Tracking**: Reports stream events back to orchestrator for monitoring

### Setup 📐

The recommended way to use acexy with orchestrator integration is via the provided `docker-compose.yml`:

```shell
wget https://raw.githubusercontent.com/Javinator9889/acexy/refs/heads/main/docker-compose.yml
docker compose up -d
```

This will start:
- **acexy** on port 8080 with orchestrator integration enabled
- **orchestrator** on port 8000 for managing acestream engines
- Automatic acestream engine provisioning as needed

### Configuration ⚙

| Environment Variable | Description | Default |
|---------------------|-------------|---------|
| `ACEXY_ORCH_URL` | Base URL for orchestrator API | _(empty - disabled)_ |
| `ACEXY_ORCH_APIKEY` | API key for orchestrator authentication | _(empty)_ |
| `ACEXY_CONTAINER_ID` | Container ID for orchestrator identification | _(auto-detected)_ |
| `ACEXY_MAX_STREAMS_PER_ENGINE` | Maximum streams allowed per engine when using orchestrator | `1` |

### Fallback Mode 🔄

If orchestrator integration is not configured or the orchestrator is unavailable, acexy will automatically fall back to the traditional single-engine mode using the configured `ACEXY_HOST` and `ACEXY_PORT`.

## Configuration Options ⚙

Acexy has tons of configuration options that allow you to customize the behavior. All of them have
default values that were tested for the optimal experience, but you may need to adjust them
to fit your needs.

> **PRO-TIP**: You can issue `acexy -help` to have a complete view of all the available options.

As Acexy was thought to be run inside a Docker container, all the variables and settings are
adjustable by using environment variables.


<table>
  <thead>
    <tr>
      <th>Flag</th>
      <th>Environment Variable</th>
      <th>Description</th>
      <th>Default</th>
    </tr>
  </thead>
  <tbody>
    <tr>
      <th><code>-license</code></th>
      <th>-</th>
      <th>Prints the program license and exits</th>
      <th>-</th>
    <tr>
    <tr>
      <th><code>-help</code></th>
      <th>-</th>
      <th>Prints the help message and exits</th>
      <th>-</th>
    <tr>
    <tr>
      <th><code>-addr</code></th>
      <th><code>ACEXY_LISTEN_ADDR</code></th>
      <th>Address where Acexy is listening to. Useful when running in <code>host</code> mode.</th>
      <th><code>:8080</code></th>
    <tr>
    <tr>
      <th><code>-scheme</code></th>
      <th><code>ACEXY_SCHEME</code></th>
      <th>
        The scheme of the AceStream middleware. If you have configured AceStream to work in HTTPS,
        you will have to tweak this value.
      </th>
      <th><code>http</code></th>
    <tr>
    <tr>
      <th><code>-acestream-host</code></th>
      <th><code>ACEXY_HOST</code></th>
      <th>
        Where the AceStream middleware is located. Used as fallback when orchestrator integration 
        is not configured or unavailable.
      </th>
      <th><code>localhost</code></th>
    <tr>
    <tr>
      <th><code>-acestream-port</code></th>
      <th><code>ACEXY_PORT</code></th>
      <th>
        The port to connect to the AceStream middleware. Used as fallback when orchestrator 
        integration is not configured or unavailable.
      </th>
      <th><code>6878</code></th>
    <tr>
    <tr>
      <th><code>-m3u8-stream-timeout</code></th>
      <th><code>ACEXY_M3U8_STREAM_TIMEOUT</code></th>
      <th>
        When running Acexy in M3U8 mode, the timeout to consider a stream is done.
      </th>
      <th><code>60s</code></th>
    <tr>
    <tr>
      <th><code>-m3u8</code></th>
      <th><code>ACEXY_M3U8</code></th>
      <th>
        Enable M3U8 mode in Acexy. <b>WARNING</b>: This mode is experimental and may not work as expected.
      </th>
      <th>Disabled</th>
    <tr>
    <tr>
      <th><code>-empty-timeout</code></th>
      <th><code>ACEXY_EMPTY_TIMEOUT</code></th>
      <th>
        Timeout to consider a stream is finished once empty information is received from
        the middleware. Useless when in M3U8 mode.
      </th>
      <th><code>1m</code></th>
    <tr>
    <tr>
      <th><code>-buffer-size</code></th>
      <th><code>ACEXY_BUFFER_SIZE</code></th>
      <th>
        Buffers up-to <code>buffer-size</code> bytes of a stream before copying the data to the
        player. Useful to have better stability during plays.
      </th>
      <th><code>4.2MiB</code></th>
    <tr>
    <tr>
      <th><code>-no-response-timeout</code></th>
      <th><code>ACEXY_NO_RESPONSE_TIMEOUT</code></th>
      <th>
        Time to wait for the AceStream middleware to return a response for a newly opened stream.
        This must be as low as possible unless your Internet connection is really bad
        (ie: You have very big latencies).
      </th>
      <th><code>1s</code></th>
    <tr>
    <tr>
      <th>-</th>
      <th><code>ACEXY_ORCH_URL</code></th>
      <th>
        Base URL for the orchestrator API. When set, enables orchestrator integration for 
        dynamic engine selection and load balancing. Leave empty to disable orchestrator integration.
      </th>
      <th><i>empty</i></th>
    <tr>
    <tr>
      <th>-</th>
      <th><code>ACEXY_ORCH_APIKEY</code></th>
      <th>
        API key for orchestrator authentication. Required if the orchestrator has API key 
        authentication enabled.
      </th>
      <th><i>empty</i></th>
    <tr>
    <tr>
      <th>-</th>
      <th><code>ACEXY_CONTAINER_ID</code></th>
      <th>
        Container ID for orchestrator identification. Usually auto-detected when running in Docker.
        Used for event reporting and engine identification.
      </th>
      <th><i>auto-detected</i></th>
    <tr>
  </tbody>
</table>

> **NOTE**: The list of options is extensive but could be outdated. Always refer to the
> Acexy binary `-help` output when in doubt.
