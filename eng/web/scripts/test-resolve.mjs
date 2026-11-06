// Lets `node --test` load the app's TypeScript as is: Node strips the types itself (22.18+),
// and this hook resolves the extensionless relative imports the app uses ("./errors") to their
// .ts file, as the bundler does. No test dependency is installed for it.
import { registerHooks } from "node:module";

registerHooks({
  resolve(specifier, context, next) {
    try {
      return next(specifier, context);
    } catch (err) {
      if (specifier.startsWith(".") && !/\.[cm]?[jt]sx?$/.test(specifier)) return next(`${specifier}.ts`, context);
      throw err;
    }
  },
});
