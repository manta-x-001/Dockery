import { config } from '@/config';

/** Registry host[:port] as the browser reaches it, without scheme. */
export function registryHost(): string {
  return config.registryUrl.replace(/^https?:\/\//, '').replace(/\/+$/, '');
}

/** Pullable reference, e.g. `registry.example.com/acme/app:v1`. */
export function imageRef(repo: string, tag: string): string {
  return `${registryHost()}/${repo}:${tag}`;
}
