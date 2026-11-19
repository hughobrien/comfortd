test:
    go test ./...

race:
    go test -race ./...

build:
    go build -o comfortd ./cmd/comfortd

gate:
    rsync -a --delete --exclude .git --exclude spike/out --exclude comfort ./ bigthink:~/comfortd-gate/
    ssh bigthink 'mkdir -p /tmp/cgate && cd ~/comfortd-gate && TMPDIR=/tmp/cgate nix build --no-write-lock-file -L "path:$PWD#checks.x86_64-linux.gate" > /tmp/cgate.log 2>&1; echo "NIX EXIT=$?"; tail -30 /tmp/cgate.log'

generate:
    go run github.com/a-h/templ/cmd/templ@v0.3.1118 generate
