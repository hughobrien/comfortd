{
  description = "comfortd — HomeKit bridge and web UI for Mitsubishi Comfort (Kumo Cloud) air conditioners";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }: let
    systems = [ "x86_64-linux" ];
    forEachSystem = f: nixpkgs.lib.genAttrs systems f;

    # nixpkgs lags templ; keep this in step with the templ version in go.mod
    # (the *_templ.go files and the drift check use it). Drop when nixpkgs catches up.
    templOverlay = final: prev: {
      templ =
        if prev.lib.versionAtLeast prev.templ.version "0.3.1118" then
          throw "nixpkgs now ships templ ${prev.templ.version}: delete templOverlay from flake.nix"
        else
          prev.templ.overrideAttrs (old: rec {
            version = "0.3.1118";
            src = prev.fetchFromGitHub {
              owner = "a-h";
              repo = "templ";
              rev = "v${version}";
              hash = "sha256-4ARBfwLD37FSCC3pgYWomsXRThl8NkHLte8awD0/Ddo=";
            };
            vendorHash = "sha256-SI3Y7eqw+sLvBmRi7r4Oc30ovIKvFRGxdXF8fyKog9g=";
          });
    };

    perSystem = forEachSystem (system: let
      pkgs = import nixpkgs { inherit system; overlays = [ templOverlay ]; };
      version = "0.1.0";

      comfortd-pkg = pkgs.buildGoModule {
        pname = "comfortd";
        inherit version;
        src = ./.;
        # When go.mod/go.sum change, set this to
        # pkgs.lib.fakeHash, run the gate build, and paste the `got:` hash.
        vendorHash = "sha256-2ZNZBeZY688ZM/cIsUF3lcyAzbACXayNDREy5UNOMag=";
        subPackages = [ "cmd/comfortd" ];
        ldflags = [ "-s" "-w" "-X main.version=${version}" ];
        doCheck = true;
        meta = with pkgs.lib; {
          description = "HomeKit bridge and web UI for Mitsubishi Comfort air conditioners";
          license = licenses.gpl3Plus;
          platforms = platforms.unix;
          mainProgram = "comfortd";
        };
      };
    in {
      packages = {
        default = comfortd-pkg;
        comfortd = comfortd-pkg;
      };

      apps.default = {
        type = "app";
        program = "${comfortd-pkg}/bin/comfortd";
      };

      devShells.default = pkgs.mkShell {
        packages = with pkgs; [ go gopls gotools just templ ];
      };

      checks.gate = comfortd-pkg.overrideAttrs (old: {
        pname = "comfortd-ci-gate";
        nativeBuildInputs = (old.nativeBuildInputs or [ ]) ++ [ pkgs.clang pkgs.templ ];
        doCheck = true;
        checkPhase = ''
          runHook preCheck
          export HOME="$TMPDIR"
          go vet ./...
          drift=$(gofmt -l . | grep -v '^vendor/' || true)
          [ -z "$drift" ] || { echo "gofmt drift: $drift"; exit 1; }
          go test ./...
          CGO_ENABLED=1 CC=clang go test -race ./...
          cp -a cmd/comfortd/ui/templates "$TMPDIR/templ-before"
          templ generate
          diff -rq "$TMPDIR/templ-before" cmd/comfortd/ui/templates || { echo "templ drift; run 'just generate'"; exit 1; }
          runHook postCheck
        '';
      });

      formatter = pkgs.nixpkgs-fmt;
    });

    moduleSystem = extra: nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        self.nixosModules.default
        {
          boot.isContainer = true;
          system.stateVersion = "26.05";
          networking.hostName = "my-host";
          services.comfortd = {
            enable = true;
            credentialsFile = "/run/keys/comfortd";
            prometheus.enable = true;
            push.offsite.url = "http://offsite:8428";
          };
        }
        extra
      ];
    };
    # Evaluation fails (so the check does) if the options stop doing their job.
    moduleCheck =
      let
        vm = (moduleSystem { services.victoriametrics.enable = true; }).config;
        prom = (moduleSystem {
          services.prometheus.enable = true;
          services.prometheus.extraFlags = [ "--web.enable-remote-write-receiver" ];
        }).config;
        # The two HomeKit settings that used to do nothing must now trip an assertion.
        bad = c: (moduleSystem { services.comfortd = c; }).config.assertions;
        failing = as: lib.filter (a: !a.assertion) as;
        lib = nixpkgs.lib;
        job = jobs: lib.findFirst (j: j.job_name == "comfortd") null jobs;
        drops = j: lib.any (r: r.source_labels == [ "pushed_my_host" ] && r.regex == "true" && r.action == "drop") j.metric_relabel_configs;
      in
      assert lib.hasInfix "my_host=http://127.0.0.1:8428" (vm.systemd.services.comfortd.serviceConfig.ExecStart);
      assert lib.hasInfix "offsite=http://offsite:8428" (vm.systemd.services.comfortd.serviceConfig.ExecStart);
      assert drops (job vm.services.victoriametrics.prometheusConfig.scrape_configs);
      assert vm.services.prometheus.scrapeConfigs == [ ];
      assert lib.hasInfix "my_host=http://127.0.0.1:9090" (prom.systemd.services.comfortd.serviceConfig.ExecStart);
      assert drops (job prom.services.prometheus.scrapeConfigs);
      assert lib.length (failing (bad { homekit.enable = true; })) == 1;
      assert lib.length (failing (bad { homekit.enable = true; homekit.units = [ "office" ]; openFirewall = true; })) == 1;
      assert failing (bad { homekit.enable = true; homekit.units = [ "office" ]; homekit.port = 51826; openFirewall = true; }) == [ ];
      vm.systemd.units."comfortd.service".unit;
    defaultModule = { pkgs, lib, ... }: {
      imports = [ ./nix/module.nix ];
      services.comfortd.package = lib.mkDefault
        self.packages.${pkgs.stdenv.hostPlatform.system}.default;
    };
  in {
    nixosModules.default = defaultModule;
    nixosModules.comfortd = defaultModule;

    packages   = forEachSystem (system: perSystem.${system}.packages);
    apps       = forEachSystem (system: perSystem.${system}.apps);
    checks     = forEachSystem (system: perSystem.${system}.checks // {
      module = moduleCheck;
    });
    devShells  = forEachSystem (system: perSystem.${system}.devShells);
    formatter  = forEachSystem (system: perSystem.${system}.formatter);
  };
}
