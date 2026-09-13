import { ConfigFormComponent } from './config-form.component';

describe('ConfigFormComponent', () => {
  let component: ConfigFormComponent;

  beforeEach(() => {
    component = new ConfigFormComponent(
      null as never,
      null as never,
      { convertHours: (value: number) => value } as never,
      { instant: (key: string) => key } as never,
      null as never,
      null as never,
      null as never,
      null as never,
      document
    );
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
