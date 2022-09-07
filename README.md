# natshook
Subscribe for NATS server and run scripts whenever the specific subject is triggered.

## Usage

```
Usage:
  natshook [OPTIONS]
Application Options:
  -a, --addr=     Address to listen on (default: :8080)
  -f, --file=     Path to the toml file containing hooks definition
      --pid=      Create PID file at the given path
Help Options:
  -h, --help      Show this help message
```


Define some hooks you want to serve in `hooks.toml`.

```
[[hooks]]
subject = 'sample'
exec = '/path/to/script.sh'
```

Run `natshook` as below:

```sh
$ natshook --file hooks.toml
2022/08/01 21:00:00 Subscribe subject 'sample'
```

Then you can execute the script by publish a message to NATS server:

```bash
$ cat input.json | nats request sample
```

Requested payload will be passed to the script as stdin.
The output result will be displayed.
