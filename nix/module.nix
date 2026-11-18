# SPDX-License-Identifier: GPL-3.0-or-later
#
# NixOS module for comfortd.
#
#   {
#     imports = [ inputs.comfortd.nixosModules.default ];
#     services.comfortd = {
#       enable = true;
#       credentialsFile = "/persist/comfortd/credentials";
#     };
#   }

{ config, lib, pkgs, ... }:

let
  cfg = config.services.comfortd;

  # Generic systemd sandboxing, nothing specific to comfortd.
  sandbox = {
    NoNewPrivileges = true;
    ProtectSystem = "strict";
    ProtectHome = true;
    PrivateTmp = true;
    PrivateDevices = true;
    ProtectKernelTunables = true;
    ProtectKernelModules = true;
    ProtectKernelLogs = true;
    ProtectControlGroups = true;
    ProtectClock = true;
    ProtectHostname = true;
    ProtectProc = "invisible";
    RestrictNamespaces = true;
    RestrictRealtime = true;
    RestrictSUIDSGID = true;
    LockPersonality = true;
    MemoryDenyWriteExecute = true;
    SystemCallArchitectures = "native";
  };
in {
  options.services.comfortd = {
    enable = lib.mkEnableOption "comfortd — Mitsubishi Comfort (Kumo Cloud) bridge and web UI";

    package = lib.mkOption {
      type = lib.types.package;
      default = pkgs.comfortd or (throw
        "services.comfortd.package is unset. Set it explicitly, e.g. `services.comfortd.package = inputs.comfortd.packages.\${pkgs.system}.default;`.");
      defaultText = lib.literalExpression "pkgs.comfortd";
      description = "The comfortd package providing /bin/comfortd.";
    };

    listen = lib.mkOption {
      type = lib.types.str;
      default = "127.0.0.1:9879";
      description = "HTTP listen address for the web UI and /v1 API.";
    };

    credentialsFile = lib.mkOption {
      type = lib.types.path;
      description = ''
        File with the Comfort login: email on the first line, password on the
        second. Read by systemd (LoadCredential), so it can be root-only.
      '';
    };

    pollInterval = lib.mkOption {
      type = lib.types.str;
      default = "60s";
      description = "How often to poll each unit.";
    };

    backstop = lib.mkOption {
      type = lib.types.str;
      default = "2m";
      description = ''
        How often to do a full poll while the Comfort app's push socket is
        connected (the socket delivers changes; this catches anything missed).
        Starts at the poll interval's order of size while the socket is proven;
        the long-run value is 15m.
      '';
    };

    homekit = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = false;
        description = "Expose the units in `homekit.units` to HomeKit.";
      };
      port = lib.mkOption {
        type = lib.types.port;
        default = 0;
        description = "HAP TCP port. 0 = ephemeral.";
      };
      units = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        description = "Unit slugs to expose to HomeKit.";
      };
    };

    limitUnits = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = ''
        Unit slugs (also in writableUnits) whose Min/Max setpoint limits can
        be changed from the web UI. Not every adapter is known to take the
        command, so each unit is opted in.
      '';
    };

    writableUnits = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = "Unit slugs allowed to receive commands (web UI and API). Empty = read-only.";
    };

    inactiveUnits = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = "Unit slugs shown as a blank placeholder in the web UI (the API still lists them).";
    };

    openFirewall = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Open the HomeKit port and mDNS UDP 5353 (HomeKit only).";
    };

    extraArgs = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = "Extra command-line arguments for comfortd.";
    };
  };

  config = lib.mkIf cfg.enable {
    users.users.comfortd = {
      isSystemUser = true;
      group = "comfortd";
      description = "comfortd daemon user";
    };
    users.groups.comfortd = { };

    systemd.services.comfortd = {
      description = "Mitsubishi Comfort (Kumo Cloud) bridge";
      wants = [ "network-online.target" ];
      after = [ "network-online.target" ];
      wantedBy = [ "multi-user.target" ];

      serviceConfig = sandbox // {
        ExecStart = lib.concatStringsSep " " ([
          "${cfg.package}/bin/comfortd"
          "--listen" (lib.escapeShellArg cfg.listen)
          "--state-dir" "/var/lib/comfortd"
          "--credentials" "%d/credentials"
          "--poll-interval" cfg.pollInterval
          "--backstop" cfg.backstop
        ] ++ lib.optionals (cfg.writableUnits != [ ]) [
          "--writable-units" (lib.escapeShellArg (lib.concatStringsSep "," cfg.writableUnits))
        ] ++ lib.optionals (cfg.limitUnits != [ ]) [
          "--limit-units" (lib.escapeShellArg (lib.concatStringsSep "," cfg.limitUnits))
        ] ++ lib.optionals (cfg.inactiveUnits != [ ]) [
          "--inactive-units" (lib.escapeShellArg (lib.concatStringsSep "," cfg.inactiveUnits))
        ] ++ lib.optionals cfg.homekit.enable [
          "--hap-port" (toString cfg.homekit.port)
          "--homekit-units" (lib.escapeShellArg (lib.concatStringsSep "," cfg.homekit.units))
        ] ++ map lib.escapeShellArg cfg.extraArgs);
        LoadCredential = [ "credentials:${cfg.credentialsFile}" ];
        User = "comfortd";
        Group = "comfortd";
        Restart = "on-failure";
        RestartSec = "10s";
        StateDirectory = "comfortd";
        StateDirectoryMode = "0700";

        # Hardening: the generic sandbox below, plus the two settings that
        # depend on what comfortd does. AF_NETLINK is kept for HomeKit mDNS
        # (Go's net.Interfaces() needs it).
        RestrictAddressFamilies = [ "AF_INET" "AF_INET6" "AF_UNIX" "AF_NETLINK" ];
        SystemCallFilter = [ "@system-service" "~@privileged" ];
      };
    };

    networking.firewall = lib.mkIf cfg.openFirewall {
      allowedUDPPorts = [ 5353 ];
      allowedTCPPorts = lib.optional (cfg.homekit.port != 0) cfg.homekit.port;
    };
  };
}
