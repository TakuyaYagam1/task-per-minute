import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: 'standalone',
  async rewrites() {
    return [
      {
        source: '/api/:path*',
        destination: process.env.BACKEND_URL ? `${process.env.BACKEND_URL}/api/:path*` : 'http://localhost:8080/api/:path*',
      },
      {
        source: '/ws',
        destination: process.env.BACKEND_URL ? `${process.env.BACKEND_URL}/ws` : 'http://localhost:8080/ws',
      },
    ];
  },
};

export default nextConfig;
