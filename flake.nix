{
  description = "libreAI — lightweight self-hosted AI chat";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in
    {
      packages = forAllSystems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          libreai = pkgs.callPackage ./deploy/nixos/package.nix {
            version = self.shortRev or self.dirtyShortRev or "dev";
          };
        in
        {
          default = libreai;
          inherit libreai;
        });

      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.libreai}/bin/libreai";
        };
      });

      nixosModules = {
        default = import ./deploy/nixos/module.nix;
        libreai = self.nixosModules.default;
      };

      formatter = forAllSystems (system: nixpkgs.legacyPackages.${system}.nixfmt-rfc-style);
    };
}
