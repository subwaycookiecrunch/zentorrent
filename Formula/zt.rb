class Zt < Formula
  desc "Lightweight torrent streaming CLI"
  homepage "https://github.com/subwaycookiecrunch/zentorrent"
  version "2.0.0"

  on_macos do
    on_arm do
      url "https://github.com/subwaycookiecrunch/zentorrent/releases/download/v2.0.0/zt-macos-arm64"
      sha256 "344885d034f0b844dd86bb92a7c778e5f49cae8b1bf54b1750cee3f773900928"
    end
    on_intel do
      url "https://github.com/subwaycookiecrunch/zentorrent/releases/download/v2.0.0/zt-macos-amd64"
      sha256 "15273a8cdc2540c5f5dea056d1ab83697f71f2156e9a49b9447602879f980d60"
    end
  end

  def install
    bin.install Dir["zt-macos-*"].first => "zt"
  end
end
