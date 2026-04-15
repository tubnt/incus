import antfu from "@antfu/eslint-config";

export default antfu({
  react: true,
  typescript: true,
  stylistic: false,
  rules: {
    "no-console": "warn",
    "react-refresh/only-export-components": "off",
  },
  ignores: ["src/app/routeTree.gen.ts", "dist/**"],
});
