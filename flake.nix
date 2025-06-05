{
  description = "Reactor - A Telegram Bot for monitoring and forwarding messages";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    let
      buildReactor = pkgs: pkgs.buildGoModule rec {
        pname = "reactor";
        version = "0.1.0";

        src = ./.;

        vendorHash = "sha256-oKoSCT2lpykWoU6dkD36XryieZb7VAdEs3S7ZrJqPbQ=";

        subPackages = [ "src" ];

        postInstall = ''
          mv $out/bin/src $out/bin/reactor
        '';

        meta = with pkgs.lib; {
          description = "A Telegram Bot for monitoring and forwarding messages";
          homepage = "https://github.com/sleroq/reactor";
          license = licenses.gpl3Only;
          maintainers = [ ];
          platforms = platforms.unix;
        };
      };
    in
    {
      nixosModules.reactor = { config, lib, pkgs, ... }:
        with lib;
        let
          cfg = config.services.reactor;
        in {
          options.services.reactor = {
            enable = mkEnableOption "Reactor Telegram Bot";

            package = mkOption {
              type = types.package;
              default = buildReactor pkgs;
              description = "The reactor package to use";
            };

            user = mkOption {
              type = types.str;
              default = "reactor";
              description = "User to run reactor as";
            };

            group = mkOption {
              type = types.str;
              default = "reactor";
              description = "Group to run reactor as";
            };

            dataDir = mkOption {
              type = types.str;
              default = "/var/lib/reactor";
              description = "Directory to store reactor data";
            };

            environmentFile = mkOption {
              type = types.nullOr types.str;
              default = null;
              description = "File containing environment variables";
            };

            settings = mkOption {
              type = types.attrs;
              default = { };
              description = "Environment variables for reactor";
            };
          };

          config = mkIf cfg.enable {
            users.users.${cfg.user} = {
              isSystemUser = true;
              group = cfg.group;
              home = cfg.dataDir;
              createHome = true;
            };

            users.groups.${cfg.group} = { };

            systemd.services.reactor = {
              description = "Reactor Telegram Bot";
              wantedBy = [ "multi-user.target" ];
              after = [ "network.target" ];

              serviceConfig = {
                Type = "simple";
                User = cfg.user;
                Group = cfg.group;
                WorkingDirectory = cfg.dataDir;
                ExecStart = "${cfg.package}/bin/reactor";
                Restart = "always";
                RestartSec = "5s";

                NoNewPrivileges = true;
                PrivateTmp = true;
                ProtectSystem = "strict";
                ProtectHome = true;
                ReadWritePaths = [ cfg.dataDir ];
                ProtectKernelTunables = true;
                ProtectKernelModules = true;
                ProtectControlGroups = true;
              } // lib.optionalAttrs (cfg.environmentFile != null) {
                EnvironmentFile = cfg.environmentFile;
              };

              environment = cfg.settings // {
                REACTOR_SESSION_DIR = cfg.dataDir;
              };
            };
          };
        };

      nixosModules.default = self.nixosModules.reactor;
    } // flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        reactor = buildReactor pkgs;
      in
      {
        packages.default = reactor;
        packages.reactor = reactor;

        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go_1_24
            gopls
            gotools
            go-tools
            
            git
            gnumake
            
            sqlite
          ];

          shellHook = ''
            echo "Welcome to Reactor development environment"
            echo "Go version: $(go version)"
            echo ""
            echo "Available commands:"
            echo "  go run src/main.go  - Run the bot"
            echo "  go build src/main.go - Build the bot"
            echo "  go test ./...       - Run tests"
            echo ""
            echo "Don't forget to set up your environment variables"
          '';

          CGO_ENABLED = "1";
        };

        apps.default = {
          type = "app";
          program = "${reactor}/bin/reactor";
        };

        formatter = pkgs.nixpkgs-fmt;
      });
}
