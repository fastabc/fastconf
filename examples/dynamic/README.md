# Watch one file

Go 1.24+, run from the repository root in a terminal:

```sh
cp examples/single-file/app.yaml /tmp/fastconf-app.yaml
go run ./examples/dynamic -file /tmp/fastconf-app.yaml
```

In another terminal, change the file:

```sh
printf 'port: 9090\n' > /tmp/fastconf-app.yaml
printf 'prot: 9091\n' > /tmp/fastconf-app.yaml
```

The program reports initial port 8080, commits 9090, then rejects the misspelled
key and retains 9090. Press Ctrl-C to close the Manager and unsubscribe. The
default directory loader also considers `conf.d` if present in the working
directory. See the complete [program](main.go) and [dynamic guide](../../docs/getting-started/dynamic.md).
