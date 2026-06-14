# minecraftinator

A Kubernetes operator that runs Minecraft servers. Create a `MinecraftServer`, get a server with a DNS entry — no other setup required.

## What it does

- Runs [`itzg/minecraft-server`](https://hub.docker.com/r/itzg/minecraft-server) in a pod with a PVC for world data
- Auto-creates a TCP proxy ([mc-router](https://github.com/itzg/mc-router)) so multiple servers share one IP on port 25565
- Routes players to the right server by hostname (mc-router reads the Minecraft handshake)
- Annotates the proxy's LoadBalancer service so ExternalDNS creates DNS records automatically
- Generates a random subdomain when you don't specify one

## Quick start

Just create a server. The proxy is created automatically.

```yaml
apiVersion: minecraft.mittwald.de/v1alpha1
kind: MinecraftServer
metadata:
  name: survival
  namespace: default
spec:
  type: PAPER
  memory: "2G"
  baseDomain: mc.feldt.systems
```

Check what domain was assigned:

```
$ kubectl get minecraftservers
NAME       VERSION   TYPE    DOMAIN                    PHASE     READY
survival   LATEST    PAPER   a3f9c1.mc.feldt.systems   Running   1
```

Players connect to `a3f9c1.mc.feldt.systems:25565`. ExternalDNS handles the DNS record.

## Spec reference

```yaml
spec:
  version: "1.21.4"          # Minecraft version, or "LATEST"
  type: PAPER                 # VANILLA, FORGE, FABRIC, PAPER, SPIGOT, PURPUR, FOLIA
  motd: "My Server"
  maxPlayers: 20
  difficulty: normal          # peaceful, easy, normal, hard
  gamemode: survival          # survival, creative, adventure, spectator
  memory: "2G"                # JVM heap size
  ops: "Notch,jeb_"          # comma-separated operator names
  whitelist: false

  # domain routing (pick one)
  baseDomain: mc.feldt.systems            # auto-assign: a3f9c1.mc.feldt.systems
  domain: survival.mc.feldt.systems       # or set an explicit hostname

  # which proxy to register with — created automatically if missing
  proxyRef: proxy             # default: "proxy"

  storage:
    size: "10Gi"
    storageClassName: longhorn

  resources:
    requests:
      cpu: "500m"
      memory: "2.5Gi"
    limits:
      cpu: "2"
      memory: "3Gi"

  # pass extra env vars to itzg/minecraft-server
  env:
    - name: ENABLE_COMMAND_BLOCK
      value: "true"
```

## Standalone mode

Skip the proxy entirely and expose the server directly:

```yaml
spec:
  disableProxy: true
  serviceType: NodePort
  nodePort: 30065
```

Players connect to `<node-ip>:30065`. No DNS record is created.

## How the routing works

```
player → survival.mc.feldt.systems:25565
             ↓  (DNS → LoadBalancer IP)
         mc-router (proxy pod)
             ↓  (reads hostname from Minecraft handshake)
         survival pod :25565
```

The proxy's LoadBalancer service gets the `external-dns.alpha.kubernetes.io/hostname` annotation updated every time a server is added or removed. ExternalDNS syncs that to Cloudflare (or wherever).

## ExternalDNS

ExternalDNS needs `--source=service` to read Service annotations. If you only have `--source=ingress`:

```
kubectl patch deployment external-dns -n kube-system --type=json \
  -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--source=service"}]'
```

## Install

```
kubectl apply -f https://github.com/mittwald/minecraftinator/releases/latest/download/install.yaml
```

Or from source:

```
make install   # installs CRDs
make deploy    # deploys the operator
```

## Build

```
make build          # compile locally
make docker-build   # build container image
make generate       # regenerate deepcopy methods after editing types
make manifests      # regenerate CRDs after editing kubebuilder markers
```
