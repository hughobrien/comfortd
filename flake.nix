{
  description = "comfortd — HomeKit bridge and web UI for Mitsubishi Comfort (Kumo Cloud) air conditioners";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }: let
    systems = [ "x86_64-linux" ];
    forEachSystem = f: nixpkgs.lib.genAttrs systems f;

    perSystem = forEachSystem (system: let
      pkgs = import nixpkgs { inherit system; };
      version = "0.1.0";

      comfortd-pkg = pkgs.buildGoModule {
        pname = "comfortd";
        inherit version;
        src = ./.;
        # No dependencies yet. When go.mod/go.sum change, set this to
        # pkgs.lib.fakeHash, run the gate build, and paste the `got:` hash.
        vendorHash = null;
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
        packages = with pkgs; [ go gopls gotools just ];
      };

      checks.gate = comfortd-pkg.overrideAttrs (old: {
        pname = "comfortd-ci-gate";
        nativeBuildInputs = (old.nativeBuildInputs or [ ]) ++ [ pkgs.clang ];
        doCheck = true;
        checkPhase = ''
          runHook preCheck
          export HOME="$TMPDIR"
          go vet ./...
          drift=$(gofmt -l . | grep -v '^vendor/' || true)
          [ -z "$drift" ] || { echo "gofmt drift: $drift"; exit 1; }
          go test ./...
          CGO_ENABLED=1 CC=clang go test -race ./...
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
