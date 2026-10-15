export default async function main (ctx) {
  const url = ctx.env.SLICC_LINK_URL
  if (url === undefined) return 1
  await ctx.write(1, `${url}\n`)
  return 0
}
