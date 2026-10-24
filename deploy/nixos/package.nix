# Nix package for libreAI.
#
# `vendorHash` is intentionally set to lib.fakeHash because Nix was not
# available in the environment where this repository was prepared. The first
# build will fail with the expected hash; replace the value below with that
# hash and build again. See README.md ("NixOS and the flake") for the exact
# procedure. The hash changes only when go.mod/go.sum dependencies change.
{
  lib,
  buildGoModule,
  stdenv,
  version ? "unstable",
}:

buildGoModule {
  pname = "libreai";
  inherit version;

  src = ../..;

  # Keep the binary usable on the ThinkPad X301 (Core 2 Duo, x86-64-v1).
  # GOAMD64 is only meaningful for amd64; other platforms ignore it.
  env = lib.optionalAttrs stdenv.hostPlatform.isx86_64 {
    GOAMD64 = "v1";
  };
  CGO_ENABLED = "0";

  subPackages = [ "cmd/libreai" ];

  ldflags = [
    "-s"
    "-w"
    "-X"
    "main.version=${version}"
  ];

  # This is the only value that must be filled in after the first Nix build.
  vendorHash = lib.fakeHash;

  doCheck = true;

  postInstall = ''
    install -Dm644 README.md "$out/share/doc/libreai/README.md"
    install -Dm644 SECURITY.md "$out/share/doc/libreai/SECURITY.md"
    install -Dm644 LICENSE "$out/share/doc/libreai/LICENSE"
  '';

  meta = {
    description = "Lightweight self-hosted AI chat with an embedded web interface";
    license = lib.licenses.mit;
    mainProgram = "libreai";
    platforms = lib.platforms.linux;
  };
}
