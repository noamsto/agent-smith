{
  description = "agent-smith — instruction-artifact improver (extractor, analyst, applier)";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        version = (builtins.fromJSON (builtins.readFile ./.claude-plugin/plugin.json)).version;
      in {
        packages.default = pkgs.buildGoModule {
          pname = "agent-smith";
          inherit version;
          ldflags = [ "-X main.version=${version}" ];
          src = ./.;
          vendorHash = null; # stdlib only
          subPackages = [ "cmd/extractor" "cmd/analyst" "cmd/applier" ];
          nativeBuildInputs = [ pkgs.makeWrapper ];
          nativeCheckInputs = [ pkgs.duckdb pkgs.git ]; # tests shell out to duckdb + git
          postInstall = ''
            for b in extractor analyst; do
              wrapProgram $out/bin/$b \
                --prefix PATH : ${pkgs.duckdb}/bin
            done
            wrapProgram $out/bin/applier \
              --prefix PATH : ${pkgs.lib.makeBinPath [ pkgs.git pkgs.gh ]}
          '';
        };

        devShells.default = pkgs.mkShell {
          packages = [ pkgs.go pkgs.gopls pkgs.go-tools pkgs.duckdb pkgs.jq pkgs.git pkgs.gh pkgs.goreleaser ];
        };
      })
    // {
      # Home Manager module: `programs.agent-smith.enable = true` puts the
      # extractor/analyst/applier binaries on PATH for the /agent-smith plugin.
      # The package self-wraps their runtime deps (duckdb, git, gh), so a
      # consumer only imports this module — no manual dep wiring.
      homeManagerModules.default = { config, lib, pkgs, ... }: {
        options.programs.agent-smith.enable =
          lib.mkEnableOption "the agent-smith engine (extractor/analyst/applier) on PATH";
        config = lib.mkIf config.programs.agent-smith.enable {
          home.packages = [ self.packages.${pkgs.stdenv.hostPlatform.system}.default ];
        };
      };
    };
}
