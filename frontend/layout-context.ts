// Context belongs to a mounted outlet, never to a global route or request.
const contexts = new WeakMap<Element, Map<any, any>>();

export function registerOutletContext(outlet: Element, context: Map<any, any>): () => void {
  contexts.set(outlet, context);
  return () => { contexts.delete(outlet); };
}

export function outletContext(outlet: Element): Map<any, any> | undefined {
  return contexts.get(outlet);
}
