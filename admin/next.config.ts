import type { NextConfig } from "next";

const backendURL = process.env.OPENPAY_BACKEND_URL ?? "http://localhost:8080";

const nextConfig: NextConfig = {
  /* config options here */
  cacheComponents: true,
  partialPrefetching: true,
  turbopack: {
    rules: {
      "*.css": {
        loaders: ["@tailwindcss/turbopack"],
        as: "*.css",
      },
    },
  },
  /** Same-origin proxy: the console calls /admin/api/* and the request is
   * forwarded to the Go backend. The admin session cookie is HttpOnly on this
   * origin, which keeps the auth story simple and avoids CORS entirely. */
  async rewrites() {
    return {
      beforeFiles: [],
      afterFiles: [
        {
          source: "/admin/api/:path*",
          destination: `${backendURL}/admin/api/:path*`,
        },
      ],
      fallback: [],
    };
  },
};

export default nextConfig;
