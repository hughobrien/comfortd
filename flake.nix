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

    moduleCheck = (nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        self.nixosModules.default
        {
          boot.isContainer = true;
          system.stateVersion = "26.05";
          services.comfortd = {
            enable = true;
            credentialsFile = "/run/keys/comfortd";
          };
        }
      ];
    }).config.systemd.units."comfortd.service".unit;
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
