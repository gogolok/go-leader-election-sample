# Consul Leader Election Sample

```shell
consul agent -ui -dev -advertise=127.0.0.1

go run main.go -logLevel debug -consulCluster http://localhost:8500
```
