{ config, lib, pkgs, ... }:

let
  cfg = config.services.libreai;
in
{
  options.services.libreai = {
    enable = lib.mkEnableOption "libreAI lightweight self-hosted AI chat";

    package = lib.mkOption {
      type = lib.types.package;
      default = pkgs.callPackage ./package.nix { };
      defaultText = lib.literalExpression "pkgs.callPackage ./package.nix { }";
      description = ''
        libreAI package to run. Override this to use a package built from a
        different source or with a completed vendorHash.
      '';
    };

    user = lib.mkOption {
      type = lib.types.str;
      default = "libreai";
      description = "Unprivileged system user that runs libreAI.";
    };

    group = lib.mkOption {
      type = lib.types.str;
      default = "libreai";
      description = "System group that runs libreAI.";
    };

    dataDir = lib.mkOption {
      type = lib.types.str;
      default = "/var/lib/libreai";
      description = ''
        Directory holding the SQLite database. The module creates it with
        mode 0700 and makes it the only writable path for the service.
      '';
    };

    host = lib.mkOption {
      type = lib.types.str;
      default = "127.0.0.1";
      description = ''
        Address libreAI binds to. Keep the default loopback address and put
        Tailscale Serve in front of it unless you have another access-control
        layer.
      '';
    };

    port = lib.mkOption {
      type = lib.types.port;
      default = 8080;
      description = "TCP port for the libreAI HTTP interface.";
    };

    environmentFile = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = "/etc/libreai/libreai.env";
      description = ''
        systemd EnvironmentFile containing provider credentials and other
        settings. It is read by systemd as root, so it can be root-owned with
        mode 0640. Set to null only if all configuration is supplied through
        `environment`.
      '';
    };

    environment = lib.mkOption {
      type = lib.types.attrsOf (lib.types.either lib.types.str lib.types.int);
      default = { };
      example = {
        CHAT_RETENTION_DAYS = 30;
        LOG_LEVEL = "info";
      };
      description = ''
        Extra non-secret environment variables. The module always sets HOST,
        PORT, and DATABASE_PATH. Do not put API keys here: Nix store values are
        readable by every user on the machine. Use `environmentFile` for
        secrets.
      '';
    };

    openFirewall = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        Open `port` in the NixOS firewall. This is only needed when `host` is
        not a loopback address. The default deployment intentionally does not
        expose the service directly.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    users.groups.${cfg.group} = { };

    users.users.${cfg.user} = {
      isSystemUser = true;
      group = cfg.group;
      home = cfg.dataDir;
      createHome = false;
    };

    systemd.tmpfiles.rules = [
      "d ${cfg.dataDir} 0700 ${cfg.user} ${cfg.group} -"
    ];

    networking.firewall.allowedTCPPorts = lib.optional cfg.openFirewall cfg.port;

    systemd.services.libreai = {
      description = "libreAI lightweight self-hosted AI chat";
      wantedBy = [ "multi-user.target" ];
      wants = [ "network-online.target" ];
      after = [ "network-online.target" ];

      environment = {
        HOST = cfg.host;
        PORT = toString cfg.port;
        DATABASE_PATH = "${cfg.dataDir}/libreai.db";
      } // lib.mapAttrs (_: value: toString value) cfg.environment;

      serviceConfig = {
        Type = "simple";
        User = cfg.user;
        Group = cfg.group;
        WorkingDirectory = cfg.dataDir;
        ExecStart = "${cfg.package}/bin/libreai";
        EnvironmentFile = lib.optional (cfg.environmentFile != null) cfg.environmentFile;

        Restart = "on-failure";
        RestartSec = "3s";
        TimeoutStopSec = "25s";
        UMask = "0077";

        # Hardening: only the data directory is writable, and the service has
        # no capabilities, no home access, and no kernel interfaces.
        NoNewPrivileges = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        ProtectClock = true;
        ProtectHostname = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectKernelLogs = true;
        ProtectControlGroups = true;
        ReadWritePaths = [ cfg.dataDir ];
        CapabilityBoundingSet = "";
        AmbientCapabilities = [ ];
        RestrictAddressFamilies = [ "AF_INET" "AF_INET6" "AF_UNIX" "AF_NETLINK" ];
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        LockPersonality = true;
        SystemCallArchitectures = "native";
        SystemCallFilter = [ "@system-service" ];
        SystemCallErrorNumber = "EPERM";
        LimitNOFILE = 1024;
        TasksMax = 64;

        StandardOutput = "journal";
        StandardError = "journal";
        SyslogIdentifier = "libreai";
      };
    };
  };
}
