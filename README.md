# minecraftinator

A Kubernetes operator that runs Minecraft servers. Point it at a domain, get a server.

## What it does

You create a `MinecraftServer` resource. The operator handles the rest:

- Spins up a [`itzg/minecraft-server`](https://hub.docker.com/r/itzg/minecraft-server) pod
- Creates a PVC for world data
- Registers the server in a shared TCP proxy ([mc-router](https://github.com/itzg/mc-router)) so multiple servers share one IP and port 25565
- Annotates the proxy's LoadBalancer service so ExternalDNS creates the DNS record automatically

If you don't set a domain, the operator generates a random subdomain like `a3f9c1.mc.yourdomain.com`.

## Quick start

Apply the proxy first (sets the base domain for auto-generated subdomains):

```yaml
apiVersion: minecraft.mittwald.de/v1alpha1
kind: MinecraftProxy
metadata:
  name: proxy
  namespace: default
spec:
  baseDomain: mc.feldt.systems
  serviceType: LoadBalancer
```

Then create a server:

```yaml
apiVersion: minecraft.mittwald.de/v1alpha1
kind: MinecraftServer
metadata:
  name: survival
  namespace: default
spec:
  version: "1.21.4"
  type: PAPER
  memory: "2G"
  # leave out domain → gets auto-assigned, e.g. a3f9c1.mc.feldt.systems
```

Check what domain was assigned:

```
kubectl get minecraftservers
NAME       VERSION   TYPE    DOMAIN                        PHASE     READY
survival   1.21.4    PAPER   a3f9c1.mc.feldt.systems       Running   1
```

Players connect to `a3f9c1.mc.feldt.systems:25565`. DNS is handled automatically via ExternalDNS + Cloudflare.

## Server options

```yaml
spec:
  version: "1.21.4"        # or "LATEST"
  type: PAPER               # VANILLA, FORGE, FABRIC, PAPER, SPIGOT, PURPUR, FOLIA
  motd: "My Server"
  maxPlayers: 20
  difficulty: normal        # peaceful, easy, normal, hard
  gamemode: survival        # survival, creative, adventure, spectator
  memory: "2G"              # JVM heap
  ops: "Notch,jeb_"        # comma-separated operator names
  whitelist: false

  # explicit domain instead of auto-generated
  domain: survival.mc.feldt.systems

  # which proxy to register with (auto-created if missing)
  proxyRef: proxy

  storage:
    size: "10Gi"
    storageClassName: longhorn   # optional

  resources:
    requests:
      cpu: "500m"
      memory: "2.5Gi"
    limits:
      cpu: "2"
      memory: "3Gi"
```

## Standalone mode (no proxy)

If you want a server with its own port instead of going through the proxy:

```yaml
spec:
  disableProxy: true
  serviceType: NodePort
  nodePort: 30065
```

Players connect to `<node-ip>:30065`. No DNS is created.

## Multiple servers, one IP

The proxy ([mc-router](https://github.com/itzg/mc-router)) reads the hostname from the Minecraft handshake packet and routes the connection to the right backend. All servers run on port 25565 behind a single LoadBalancer IP.

```
player → survival.mc.feldt.systems:25565 → proxy → survival pod
player → creative.mc.feldt.systems:25565 → proxy → creative pod
```

The proxy's LoadBalancer service gets `external-dns.alpha.kubernetes.io/hostname` set to all registered hostnames. ExternalDNS picks that up and creates the A records.

## ExternalDNS setup

ExternalDNS needs `--source=service` to read Service annotations. If you're only watching Ingresses, patch it:

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
make build          # compile
make docker-build   # build image
make generate       # regenerate deepcopy after type changes
make manifests      # regenerate CRDs after marker changes
```
