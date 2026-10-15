/** A record with a health flag. */
export interface Environment {
  name: string;
  health: Health;
}

/** Health states an environment can report. */
export type Health = 'ok' | 'degraded';

/** Repository of environments. */
export interface EnvironmentRepo {
  /** Loads one environment by name. */
  load(name: string): Environment | undefined;
}

/** In-memory EnvironmentRepo. */
export class MemoryRepo implements EnvironmentRepo {
  private items = new Map<string, Environment>();
  load(name: string): Environment | undefined {
    return this.items.get(name);
  }
  /** Stores an environment. */
  save(env: Environment): void {
    this.items.set(env.name, env);
  }
}

export function isHealthy(env: Environment): boolean {
  return env.health === 'ok';
}

export const apiUrl = () => process.env.API_URL;
