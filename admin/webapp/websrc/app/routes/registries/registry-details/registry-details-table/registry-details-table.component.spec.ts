import { RegistryDetailsTableComponent } from './registry-details-table.component';

describe('RegistryDetailsTableComponent', () => {
  let component: RegistryDetailsTableComponent;

  beforeEach(() => {
    component = new RegistryDetailsTableComponent(
      null as never,
      { instant: (key: string) => key } as never,
      null as never
    );
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
