// Planted class-1 candidate: silently swallows errors.
// The integration test's stub executor cites this file from
// review-zone, emitting one class-1 finding without actually
// analyzing the code.
export async function unsafeFetch(url: string): Promise<unknown> {
  try {
    const res = await fetch(url);
    return await res.json();
  } catch {
    return null;
  }
}
