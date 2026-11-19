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

  # The local metrics store, if any: VictoriaMetrics, else Prometheus. Both
  # take remote write at <url>/api/v1/write and answer PromQL at <url>/api/v1.
  vm = config.services.victoriametrics;
  prom = config.services.prometheus;
  store = if vm.enable then "victoriametrics" else if prom.enable then "prometheus" else null;
  storeUrl =
    if store == "victoriametrics" then
      "http://${lib.optionalString (lib.hasPrefix ":" vm.listenAddress) "127.0.0.1"}${vm.listenAddress}"
    else
      "http://${if prom.listenAddress == "0.0.0.0" || prom.listenAddress == "" then "127.0.0.1" else prom.listenAddress}:${toString prom.port}";
  pushLocal = cfg.prometheus.enable && store != null;
  targets = cfg.push // lib.optionalAttrs pushLocal { ${cfg.prometheus.pushName}.url = cfg.prometheus.url; };
  validName = n: builtins.match "[a-zA-Z_][a-zA-Z0-9_]*" n != null;

  # /metrics marks each reading it also pushed with pushed_<name>="true", so
  # the local store's scrape drops the copies it already has; any other
  # scraper keeps them.
  scrapeJob = {
    job_name = cfg.prometheus.jobName;
    scrape_interval = cfg.prometheus.scrapeInterval;
    static_configs = [{ targets = [ cfg.listen ]; }];
    metric_relabel_configs = lib.optional pushLocal {
      source_labels = [ "pushed_${cfg.prometheus.pushName}" ];
      regex = "true";
      action = "drop";
    };
  };

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

    push = lib.mkOption {
      type = lib.types.attrsOf (lib.types.submodule {
        options.url = lib.mkOption {
          type = lib.types.str;
          example = "http://metrics.example:8428";
          description = "Base URL of a remote write receiver; comfortd pushes to `<url>/api/v1/write`.";
        };
      });
      default = { };
      example = lib.literalExpression ''{ offsite.url = "http://metrics.example:8428"; }'';
      description = ''
        Remote write targets, by name, each sent each unit's state whenever it changes (and each minute while fresh). /metrics
        labels each reading it also pushed `pushed_<name>="true"`; the scrape
        job on that target's host should drop those (`metric_relabel_configs`,
        action `drop`), as the one this module registers locally does. Names
        must be letters, digits and `_`, not starting with a digit.
      '';
    };

    prometheus = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = false;
        description = ''
          When true and a local metrics store is enabled
          (`services.victoriametrics`, else `services.prometheus`), push to it
          as target `pushName` and register a scrape job for /metrics that
          drops the readings it was pushed. Prometheus needs
          `--web.enable-remote-write-receiver` in its `extraFlags`.
        '';
      };
      pushName = lib.mkOption {
        type = lib.types.str;
        default = lib.replaceStrings [ "-" "." ] [ "_" "_" ] config.networking.hostName;
        defaultText = lib.literalExpression ''the host name, with - and . as _'';
        description = "Target name for the local store (the `pushed_<name>` label).";
      };
      url = lib.mkOption {
        type = lib.types.str;
        default = storeUrl;
        defaultText = lib.literalExpression ''"http://<listen address of services.victoriametrics or services.prometheus>"'';
        description = "Base URL of the local store.";
      };
      jobName = lib.mkOption {
        type = lib.types.str;
        default = "comfortd";
        description = "job_name for the auto-registered scrape, and the job label on pushed series.";
      };
      scrapeInterval = lib.mkOption {
        type = lib.types.str;
        default = "60s";
        description = "Scrape interval for /metrics.";
      };
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
      after = [ "network-online.target" ] ++ lib.optional (store != null) "${store}.service";
      wantedBy = [ "multi-user.target" ];

      serviceConfig = sandbox // {
        ExecStart = lib.concatStringsSep " " ([
          "${cfg.package}/bin/comfortd"
          "--listen" (lib.escapeShellArg cfg.listen)
          "--state-dir" "/var/lib/comfortd"
          "--credentials" "%d/credentials"
          "--poll-interval" cfg.pollInterval
          "--backstop" cfg.backstop
          "--job" (lib.escapeShellArg cfg.prometheus.jobName)
        ] ++ lib.concatLists (lib.mapAttrsToList (n: t: [ "--push" (lib.escapeShellArg "${n}=${t.url}") ]) targets) ++ lib.optionals (cfg.inactiveUnits != [ ]) [
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

    assertions = [
      {
        # Prometheus refuses the flag repeated and other exporters push too,
        # so the host sets it once; only check it is there.
        assertion = !(pushLocal && store == "prometheus")
          || lib.elem "--web.enable-remote-write-receiver" prom.extraFlags;
        message = "services.comfortd.prometheus.enable pushes to Prometheus: add \"--web.enable-remote-write-receiver\" to services.prometheus.extraFlags.";
      }
      {
        assertion = lib.all validName (lib.attrNames targets);
        message = "services.comfortd push target names must be letters, digits and _, not starting with a digit: ${lib.concatStringsSep ", " (lib.attrNames targets)}";
      }
    ];

    services.prometheus.scrapeConfigs = lib.mkIf (pushLocal && store == "prometheus") [ scrapeJob ];
    services.victoriametrics.prometheusConfig.scrape_configs = lib.mkIf (pushLocal && store == "victoriametrics") [ scrapeJob ];

    networking.firewall = lib.mkIf cfg.openFirewall {
      allowedUDPPorts = [ 5353 ];
      allowedTCPPorts = lib.optional (cfg.homekit.port != 0) cfg.homekit.port;
    };
  };
}
