{
  description = "server-mgr dev shell";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }: let
    system = "x86_64-linux";
    pkgs = nixpkgs.legacyPackages.${system};
  in {
    devShells.${system}.default = pkgs.mkShell {
      buildInputs = with pkgs; [
        go
        gopls        # LSP
        gotools      # goimports 等
        delve        # 调试器
      ];

      shellHook = ''
        export GOPATH=$HOME/go
        export GOMODCACHE=$HOME/go/pkg/mod
        export PATH=$GOPATH/bin:$PATH
        if [ ! -f go.mod ]; then
          go mod init server-mgr
          go get github.com/spf13/cobra
          go mod tidy
        fi
        echo "Go $(go version | awk '{print $3}')"
      '';
    };
  };
}