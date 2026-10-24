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
        Configuration file passed to libreAI with `-env`. It contains provider
        credentials and other settings. The application reads it as the
        service user, so it must be readable by that user; `root:libreai` with
        mode 0640 is sufficient. Set to null only if all configuration is
        supplied through `environment`.
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
        Open `port` on every interface in the NixOS firewall. This is only
        needed when `host` is not a loopback address. The default deployment
        intentionally does not expose the service directly.

        Prefer `tailscaleOnly` on a Tailscale host: it opens the port only on
        `tailscale0` instead of every interface.
      '';
    };

    tailscaleOnly = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        Allow direct connections only from the Tailscale interface
        (`services.tailscale.interfaceName`, normally `tailscale0`). Set
        `host = "0.0.0.0"` (or the machine's tailnet IP) so the server listens
        on that interface, then reach it at `http://<tailnet-ip>:<port>`.

        This opens `port` only on the Tailscale interface, not on the LAN.
        Prefer Tailscale Serve (the default loopback `host`) when possible: it
        provides HTTPS and tailnet identity.
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
    networking.firewall.interfaces.${config.services.tailscale.interfaceName}.allowedTCPPorts =
      lib.optional cfg.tailscaleOnly cfg.port;

    assertions = [
      {
        assertion = !(cfg.openFirewall && cfg.tailscaleOnly);
        message = "services.libreai: openFirewall and tailscaleOnly are mutually exclusive; tailscaleOnly already opens the port on tailscale0";
      }
      {
        assertion = !cfg.tailscaleOnly || !(cfg.host == "127.0.0.1" || cfg.host == "::1" || cfg.host == "localhost");
        message = "services.libreai: tailscaleOnly requires host = \"0.0.0.0\" (or the tailnet IP); loopback cannot accept remote connections";
      }
    ];

    systemd.services.libreai = {
      description = "libreAI lightweight self-hosted AI chat";
      wantedBy = [ "multi-user.target" ];
      wants = [ "network-online.target" ] ++ lib.optional cfg.tailscaleOnly "tailscaled.service";
      after = [ "network-online.target" ] ++ lib.optional cfg.tailscaleOnly "tailscaled.service";

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
        # Let libreAI read the file itself instead of using systemd's
        # EnvironmentFile. systemd environment files override Environment=,
        # which would silently defeat the HOST/PORT/DATABASE_PATH values set
        # above. Passing -env keeps the app's precedence: process environment
        # (module settings) over file settings.
        ExecStart = lib.concatStringsSep " " (
          [ "${cfg.package}/bin/libreai" ]
          ++ lib.optionals (cfg.environmentFile != null) [
            "-env"
            (lib.escapeShellArg cfg.environmentFile)
          ]
        );

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
